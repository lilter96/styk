package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

var _ orders.IngestionClaimer = (*Store)(nil)

func (s *Store) ClaimOrderIngestion(ctx context.Context, req orders.IngestionClaimRequest) (*orders.IngestionLease, error) {
	duration := req.LeaseUntil.Sub(req.Now)
	if req.Scope.Validate() != nil || !ordersRequestKey(req.Stream) || !ordersRequestKey(req.RefreshKey) || !ordersRequestKey(req.WorkerID) || duration <= 0 || duration > 5*time.Minute {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ordersError("begin order ingestion claim", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, req.Scope); err != nil {
		return nil, err
	}
	if err = ordersCredentialLocks(ctx, tx, req.Scope); err != nil {
		return nil, err
	}
	lease, err := claimOrderIngestionLocked(ctx, tx, req, duration)
	if err != nil {
		return nil, err
	}
	return lease, ordersRowsError("commit order ingestion claim", tx.Commit(ctx))
}

func claimOrderIngestionLocked(ctx context.Context, tx pgx.Tx, req orders.IngestionClaimRequest, duration time.Duration) (*orders.IngestionLease, error) {
	config, err := readOrderConfig(ctx, tx, req.Scope)
	if err != nil || config.Revision == 0 {
		return nil, orders.ErrNotFound
	}
	revisions, err := ordersCredentials(ctx, tx, req.Scope)
	if err != nil {
		return nil, err
	}
	if revisions.Ozon < 1 || revisions.Moysklad < 1 {
		return nil, orders.ErrBlocked
	}
	token, err := ordersID()
	if err != nil {
		return nil, err
	}
	lease := orders.IngestionLease{Scope: req.Scope, Stream: req.Stream, RefreshKey: req.RefreshKey, Token: token, ConfigRevision: config.Revision, Credentials: revisions}
	args := append(ordersScopeArgs(req.Scope), req.Stream, req.RefreshKey, token, req.WorkerID, config.Revision, revisions.Ozon, revisions.Moysklad, duration.String())
	err = tx.QueryRow(ctx, `INSERT INTO orders_ingestion_leases(tenant_id,moysklad_account,ozon_account,stream,refresh_key,sequence,token,worker_id,config_revision,ozon_revision,moysklad_revision,lease_until,updated_at) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8,$9,$10,clock_timestamp()+$11::interval,clock_timestamp()) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET stream=EXCLUDED.stream,refresh_key=EXCLUDED.refresh_key,sequence=orders_ingestion_leases.sequence+1,token=EXCLUDED.token,worker_id=EXCLUDED.worker_id,config_revision=EXCLUDED.config_revision,ozon_revision=EXCLUDED.ozon_revision,moysklad_revision=EXCLUDED.moysklad_revision,lease_until=EXCLUDED.lease_until,updated_at=EXCLUDED.updated_at WHERE orders_ingestion_leases.token IS NULL OR orders_ingestion_leases.lease_until<=clock_timestamp() RETURNING sequence,lease_until`, args...).Scan(&lease.Sequence, &lease.LeaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ordersError("claim serial order ingestion", err)
	}
	return &lease, nil
}

func lockOrderIngestion(ctx context.Context, tx pgx.Tx, page orders.SourcePage) (orders.IngestionLease, error) {
	lease := orders.IngestionLease{Scope: page.Scope}
	err := tx.QueryRow(ctx, `SELECT stream,refresh_key,sequence,token,config_revision,ozon_revision,moysklad_revision,lease_until FROM orders_ingestion_leases WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND token IS NOT NULL FOR UPDATE`, ordersScopeArgs(page.Scope)...).Scan(&lease.Stream, &lease.RefreshKey, &lease.Sequence, &lease.Token, &lease.ConfigRevision, &lease.Credentials.Ozon, &lease.Credentials.Moysklad, &lease.LeaseUntil)
	if err != nil {
		return lease, ordersError("lock order ingestion fence", err)
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return lease, err
	}
	if page.FenceToken == "" || page.FenceToken != lease.Token || page.Stream != lease.Stream || page.RefreshKey != lease.RefreshKey || page.ConfigRevision != lease.ConfigRevision || page.Credentials != lease.Credentials || !lease.LeaseUntil.After(now) {
		return lease, orders.ErrStale
	}
	return lease, nil
}

func (s *Store) RenewOrderIngestion(ctx context.Context, lease orders.IngestionLease, until time.Time) (bool, error) {
	if lease.Scope.Validate() != nil || lease.Token == "" {
		return false, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, ordersError("begin order ingestion renewal", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, lease.Scope); err != nil {
		return false, err
	}
	if err = ordersCredentialLocks(ctx, tx, lease.Scope); err != nil {
		return false, err
	}
	valid, err := renewOrderIngestionLocked(ctx, tx, lease, until)
	if err != nil {
		return false, err
	}
	return valid, ordersRowsError("commit order ingestion renewal", tx.Commit(ctx))
}

func renewOrderIngestionLocked(ctx context.Context, tx pgx.Tx, lease orders.IngestionLease, until time.Time) (bool, error) {
	page := orders.SourcePage{Scope: lease.Scope, Stream: lease.Stream, RefreshKey: lease.RefreshKey, FenceToken: lease.Token, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials}
	current, err := lockOrderIngestion(ctx, tx, page)
	if errors.Is(err, orders.ErrStale) || errors.Is(err, orders.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.Sequence != lease.Sequence {
		return false, nil
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return false, err
	}
	if !until.After(now) || until.Sub(now) > 5*time.Minute {
		return false, orders.ErrInvalid
	}
	if _, err = validateOrderPageCurrent(ctx, tx, page); err != nil {
		return false, err
	}
	args := append(ordersScopeArgs(lease.Scope), lease.Token, until)
	_, err = tx.Exec(ctx, `UPDATE orders_ingestion_leases SET lease_until=$5,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND token=$4`, args...)
	return err == nil, ordersRowsError("renew order ingestion fence", err)
}

func (s *Store) ReleaseOrderIngestion(ctx context.Context, lease orders.IngestionLease) error {
	if lease.Scope.Validate() != nil || lease.Token == "" {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	args := append(ordersScopeArgs(lease.Scope), lease.Token, lease.Sequence)
	tag, err := s.Pool.Exec(ctx, `UPDATE orders_ingestion_leases SET token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND token=$4 AND sequence=$5`, args...)
	if err != nil {
		return ordersError("release order ingestion fence", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrStale
	}
	return nil
}
