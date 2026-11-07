package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

// Retry keeps the committed cursor and pages. Changed snapshots require a new job.
func (s *Store) RetryProductMatchingJob(ctx context.Context, scope catalog.Scope, id, actor string) error {
	if scope.Validate() != nil {
		return catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackMerchantMutation(tx)
	var state, after string
	var revision, mi, oi int64
	err = tx.QueryRow(ctx, `SELECT state,cursor,revision,moy_import,ozon_import FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5 FOR UPDATE`, append(catalogScopeArgs(scope), id, actor)...).Scan(&state, &after, &revision, &mi, &oi)
	if err != nil {
		return catalogError("read matching retry", err)
	}
	if err = lockProductMatch(ctx, tx, scope, actor); err != nil {
		return err
	}
	if state == "QUEUED" || state == "RUNNING" || state == "COMPLETED" {
		return tx.Commit(ctx)
	}
	if state != "FAILED" {
		return catalogsync.ErrStale
	}
	if err = validateProductMatchingRetry(ctx, tx, scope, revision, mi, oi, after); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET state='QUEUED',code='',updated_at=clock_timestamp() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func validateProductMatchingRetry(ctx context.Context, tx pgx.Tx, scope catalog.Scope, revision, mi, oi int64, after string) error {
	policy, err := readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return err
	}
	if policy.Revision != revision {
		return catalogsync.ErrStale
	}
	data, err := productMatchDataPage(ctx, tx, scope, after)
	if err != nil {
		return err
	}
	if data.MoyID != mi || data.OzonID != oi {
		return catalogsync.ErrStale
	}
	return nil
}
