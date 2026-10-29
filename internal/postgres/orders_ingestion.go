package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) OrderCheckpoint(ctx context.Context, scope orders.Scope, stream string) (orders.Checkpoint, error) {
	if scope.Validate() != nil || !ordersRequestKey(stream) {
		return orders.Checkpoint{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderCheckpoint(ctx, s.Pool, scope, stream)
}

func readOrderCheckpoint(ctx context.Context, q ordersQuerier, scope orders.Scope, stream string) (orders.Checkpoint, error) {
	checkpoint := orders.Checkpoint{Scope: scope, Stream: stream}
	var raw []byte
	args := append(ordersScopeArgs(scope), stream)
	err := q.QueryRow(ctx, `SELECT checkpoint FROM orders_checkpoints WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND stream=$4`, args...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, nil
	}
	if err != nil {
		return checkpoint, ordersError("read order ingestion checkpoint", err)
	}
	return checkpoint, ordersDecode(raw, &checkpoint)
}

func (s *Store) CommitOrderPage(ctx context.Context, page orders.SourcePage) (orders.Checkpoint, error) {
	if err := page.Validate(); err != nil {
		return orders.Checkpoint{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.Checkpoint{}, ordersError("begin order ingestion page", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, page.Scope); err != nil {
		return orders.Checkpoint{}, err
	}
	checkpoint, err := commitOrderPageLocked(ctx, tx, page)
	if err != nil {
		return orders.Checkpoint{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return orders.Checkpoint{}, ordersError("commit order ingestion page", err)
	}
	return checkpoint, nil
}

func commitOrderPageLocked(ctx context.Context, tx pgx.Tx, page orders.SourcePage) (orders.Checkpoint, error) {
	if err := ordersCredentialLocks(ctx, tx, page.Scope); err != nil {
		return orders.Checkpoint{}, err
	}
	hash, err := orderPageHash(page)
	if err != nil {
		return orders.Checkpoint{}, err
	}
	replay, err := replayOrderPage(ctx, tx, page, hash)
	if err == nil {
		return replay, nil
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return orders.Checkpoint{}, err
	}
	config, err := validateOrderPageCurrent(ctx, tx, page)
	if err != nil {
		return orders.Checkpoint{}, err
	}
	return writeOrderPage(ctx, tx, page, config, hash)
}

func orderPageHash(page orders.SourcePage) (string, error) {
	type secretDigest struct {
		Secret orders.PostingSecret
		Hash   string
	}
	secrets := make([]secretDigest, 0, len(page.Secrets))
	for _, secret := range page.Secrets {
		hash, err := ordersHash(secret.Ciphertext)
		if err != nil {
			return "", err
		}
		secrets = append(secrets, secretDigest{secret, hash})
	}
	return ordersHash(struct {
		Page    orders.SourcePage
		Secrets []secretDigest
	}{page, secrets})
}

func replayOrderPage(ctx context.Context, tx pgx.Tx, page orders.SourcePage, hash string) (orders.Checkpoint, error) {
	var checkpoint orders.Checkpoint
	var raw []byte
	var previous string
	args := append(ordersScopeArgs(page.Scope), page.Stream, page.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT checkpoint,request_hash FROM orders_ingestion_pages WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND stream=$4 AND request_key=$5`, args...).Scan(&raw, &previous)
	if err != nil {
		return checkpoint, ordersError("read order ingestion replay", err)
	}
	if previous != hash {
		return checkpoint, orders.ErrConflict
	}
	return checkpoint, ordersDecode(raw, &checkpoint)
}

func validateOrderPageCurrent(ctx context.Context, tx pgx.Tx, page orders.SourcePage) (orders.Config, error) {
	config, err := readOrderConfig(ctx, tx, page.Scope)
	if err != nil {
		return config, err
	}
	revisions, err := ordersCredentials(ctx, tx, page.Scope)
	if err != nil {
		return config, err
	}
	if config.Revision != page.ConfigRevision || revisions != page.Credentials || revisions.Ozon < 1 || revisions.Moysklad < 1 {
		return config, orders.ErrStale
	}
	if len(page.Secrets) > 0 && !config.PIIEnabled {
		return config, orders.ErrBlocked
	}
	return config, nil
}

func writeOrderPage(ctx context.Context, tx pgx.Tx, page orders.SourcePage, config orders.Config, hash string) (orders.Checkpoint, error) {
	if _, err := lockOrderIngestion(ctx, tx, page); err != nil {
		return orders.Checkpoint{}, err
	}
	previous, err := readOrderCheckpoint(ctx, tx, page.Scope, page.Stream)
	if err != nil {
		return previous, err
	}
	if err = validateOrderCheckpoint(page, previous); err != nil {
		return previous, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return previous, err
	}
	ids, err := insertOrderPagePostings(ctx, tx, page, now)
	if err != nil {
		return previous, err
	}
	for _, secret := range page.Secrets {
		if err = insertOrderSecret(ctx, tx, secret, config, now); err != nil {
			return previous, err
		}
	}
	checkpoint := orders.Checkpoint{Scope: page.Scope, Stream: page.Stream, RefreshKey: page.RefreshKey, ConfigRevision: page.ConfigRevision, Credentials: page.Credentials, Revision: previous.Revision + 1, Cursor: page.NextCursor, WindowFrom: page.WindowFrom, WindowTo: page.WindowTo, Complete: page.Complete, UpdatedAt: now}
	if err = insertOrderCheckpoint(ctx, tx, page, checkpoint, ids, hash); err != nil {
		return previous, err
	}
	return checkpoint, finishOrderIngestionPage(ctx, tx, page)
}

func validateOrderCheckpoint(page orders.SourcePage, previous orders.Checkpoint) error {
	if previous.Revision != page.ExpectedCheckpointRevision {
		return orders.ErrStale
	}
	if previous.Revision == 0 {
		if page.ExpectedCursor != "" {
			return orders.ErrStale
		}
		return nil
	}
	if page.RefreshKey != previous.RefreshKey {
		reconfigured := previous.ConfigRevision != page.ConfigRevision || previous.Credentials != page.Credentials
		if (!previous.Complete && !reconfigured) || page.ExpectedCursor != "" {
			return orders.ErrStale
		}
		return nil
	}
	if previous.Complete || previous.Cursor != page.ExpectedCursor || previous.ConfigRevision != page.ConfigRevision || previous.Credentials != page.Credentials || !previous.WindowFrom.Equal(page.WindowFrom) || !previous.WindowTo.Equal(page.WindowTo) {
		return orders.ErrStale
	}
	return nil
}

func insertOrderPagePostings(ctx context.Context, tx pgx.Tx, page orders.SourcePage, now time.Time) ([]string, error) {
	var sequence int64
	err := tx.QueryRow(ctx, `UPDATE orders_ingestion_leases SET collection_sequence=collection_sequence+1 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 RETURNING collection_sequence`, ordersScopeArgs(page.Scope)...).Scan(&sequence)
	if err != nil {
		return nil, ordersError("sequence order collection", err)
	}
	ids := make([]string, 0, len(page.Postings))
	for _, posting := range page.Postings {
		posting.Evidence.CollectionID = fmt.Sprintf("collection:%d", sequence)
		posting.Evidence.LocalSequence = sequence
		posting.Evidence.CheckedAt = now
		posting.Evidence.CollectionKind = orders.CollectionSnapshot
		if page.FreshDetail {
			posting.Evidence.CollectionKind = orders.CollectionSerialDetail
		}
		id, err := insertOrderPosting(ctx, tx, posting, now)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func finishOrderIngestionPage(ctx context.Context, tx pgx.Tx, page orders.SourcePage) error {
	if !page.Complete {
		return nil
	}
	args := append(ordersScopeArgs(page.Scope), page.FenceToken)
	_, err := tx.Exec(ctx, `UPDATE orders_ingestion_leases SET token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND token=$4`, args...)
	return ordersRowsError("finish order collection fence", err)
}

func insertOrderCheckpoint(ctx context.Context, tx pgx.Tx, page orders.SourcePage, checkpoint orders.Checkpoint, ids []string, hash string) error {
	raw, err := ordersJSON(checkpoint)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(page.Scope), page.Stream, checkpoint.Revision, raw, checkpoint.UpdatedAt)
	if _, err = tx.Exec(ctx, `INSERT INTO orders_checkpoints(tenant_id,moysklad_account,ozon_account,stream,revision,checkpoint,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,moysklad_account,ozon_account,stream) DO UPDATE SET revision=EXCLUDED.revision,checkpoint=EXCLUDED.checkpoint,updated_at=EXCLUDED.updated_at`, args...); err != nil {
		return ordersError("save order ingestion checkpoint", err)
	}
	id, err := ordersID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_ingestion_pages(id,tenant_id,moysklad_account,ozon_account,stream,request_key,request_hash,checkpoint_revision,checkpoint,observation_ids,started_at,finished_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, page.Scope.Tenant, page.Scope.MoyskladAccount, page.Scope.OzonAccount, page.Stream, page.IdempotencyKey, hash, checkpoint.Revision, raw, ids, page.StartedAt, page.FinishedAt)
	return ordersRowsError("journal order ingestion page", err)
}
