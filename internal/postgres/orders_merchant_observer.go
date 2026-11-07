package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// The native read and transition journal commit under the existing scope lease
// and page transaction. First reads after consent/credential changes seed only.
func observeOrderMerchantIntent(ctx context.Context, tx pgx.Tx, page orders.SourcePage, read orders.NativeOrderPointRead, now time.Time) error {
	if read.Point == nil {
		return nil
	}
	config, err := readOrderConfig(ctx, tx, page.Scope)
	if err != nil {
		return err
	}
	if config.Lifecycle == nil || !config.Lifecycle.Active(now, config.Scope, config.Revision) {
		return nil
	}
	before, err := priorMerchantPoint(ctx, tx, page, read)
	if errors.Is(err, orders.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	review := orders.ReviewContext{Config: config}
	if err = enrichOrderNativeStateBaselines(ctx, tx, &review, []string{read.Correlation.PostingNumber}); err != nil {
		return err
	}
	scheme, err := merchantPostingScheme(ctx, tx, config, read.Correlation.PostingNumber, now)
	if err != nil {
		return err
	}
	intents := orders.ObserveMerchantTransitions(before, *read.Point, config, review.NativeStateBaselines[read.Correlation.PostingNumber], scheme, now)
	for _, intent := range intents {
		if err = insertMerchantIntent(ctx, tx, intent, now); err != nil {
			return err
		}
	}
	return nil
}

func merchantPostingScheme(ctx context.Context, q ordersQuerier, config orders.Config, number string, now time.Time) (string, error) {
	posting, err := readOrderPosting(ctx, q, config.Scope, number)
	if err != nil {
		return "", err
	}
	if posting.Scheme.IsKnown() {
		return *posting.Scheme.Value, nil
	}
	mappings, err := readOrderWarehouseMappings(ctx, q, config.Scope)
	if err != nil {
		return "", err
	}
	warehouse, err := (orders.ReviewContext{Config: config, WarehouseMappings: mappings}).PostingWarehouse(posting)
	if err != nil || now.Before(warehouse.Capability.ObservedAt) || !now.Before(warehouse.Capability.ValidUntil) {
		return "", nil
	}
	return warehouse.Capability.Scheme, nil
}

func priorMerchantPoint(ctx context.Context, q ordersQuerier, page orders.SourcePage, read orders.NativeOrderPointRead) (orders.NativeOrderPoint, error) {
	var point orders.NativeOrderPoint
	var raw []byte
	var revision int64
	var started, finished, observed time.Time
	var credential int64
	err := q.QueryRow(ctx, `SELECT s.point,(p.checkpoint->>'config_revision')::bigint,r.started_at,r.finished_at,COALESCE(r.observed_at,r.finished_at),r.credential_revision FROM orders_native_point_reads r
 LEFT JOIN orders_native_point_snapshots s ON s.id=r.snapshot_id JOIN orders_ingestion_pages p ON p.id=r.page_id
 WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 AND r.posting_number=$4
 ORDER BY r.admitted_at DESC,r.finished_at DESC,r.id DESC LIMIT 1`, append(ordersScopeArgs(page.Scope), read.Correlation.PostingNumber)...).Scan(&raw, &revision, &started, &finished, &observed, &credential)
	if err != nil {
		return point, ordersError("read prior merchant state point", err)
	}
	if len(raw) == 0 || revision != page.ConfigRevision {
		return point, orders.ErrNotFound
	}
	if err = ordersDecode(raw, &point); err != nil {
		return point, err
	}
	point.StartedAt, point.FinishedAt, point.ObservedAt, point.CredentialRevision = started, finished, observed, credential
	return point, nil
}

func insertMerchantIntent(ctx context.Context, tx pgx.Tx, intent orders.MerchantIntent, now time.Time) error {
	if err := intent.Validate(); err != nil {
		return err
	}
	raw, err := ordersJSON(intent)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(intent.Scope), intent.ID, intent.Correlation.PostingNumber, intent.Correlation.CustomerOrderID, intent.ConfigRevision, intent.Digest, raw, now)
	_, err = tx.Exec(ctx, `INSERT INTO orders_merchant_intents(tenant_id,moysklad_account,ozon_account,id,posting_number,customer_order_id,config_revision,digest,intent,state,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'OBSERVED',$10) ON CONFLICT DO NOTHING`, args...)
	return ordersRowsError("journal merchant state transition", err)
}
