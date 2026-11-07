package postgres

import (
	"context"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

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
	if err = catalogActor(ctx, tx, scope.Tenant, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state=CASE WHEN apply_state='FAILED' THEN 'QUEUED' ELSE apply_state END,apply_code=CASE WHEN apply_state='FAILED' THEN '' ELSE apply_code END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, append(catalogScopeArgs(scope), id, actor)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return catalogsync.ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	_, err = s.executeProductMatchingApplication(ctx, id)
	return err
}
