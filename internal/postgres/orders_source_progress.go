package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// RecordOrderSourceRefresh observes durable traversal checkpoints. A completed
// catalog import alone never claims that order discovery also succeeded.
func (s *Store) RecordOrderSourceRefresh(ctx context.Context, scope orders.Scope, collectErr error) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin order source progress", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, scope); err != nil {
		return err
	}
	code := ""
	if collectErr != nil {
		code = "SOURCE_READ_FAILED"
	}
	args := append(ordersScopeArgs(scope), code)
	if err = updateOrderSourceProgress(ctx, tx, args); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_source_refreshes SET finished_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND state IN('SUCCEEDED','FAILED','PARTIAL') AND finished_at IS NULL`, ordersScopeArgs(scope)...)
	if err != nil {
		return ordersError("finish order source progress", err)
	}
	_, err = tx.Exec(ctx, `UPDATE orders_configs c SET next_run_at=LEAST(next_run_at,clock_timestamp()+interval '1 second') WHERE c.tenant_id=$1 AND c.moysklad_account=$2 AND c.ozon_account=$3 AND EXISTS(SELECT 1 FROM orders_source_refreshes f WHERE f.tenant_id=c.tenant_id AND f.moysklad_account=c.moysklad_account AND f.ozon_account=c.ozon_account AND f.state IN('QUEUED','READING'))`, ordersScopeArgs(scope)...)
	if err != nil {
		return ordersError("continue bounded order source traversal", err)
	}
	return ordersRowsError("commit order source progress", tx.Commit(ctx))
}

func updateOrderSourceProgress(ctx context.Context, tx pgx.Tx, args []any) error {
	_, err := tx.Exec(ctx, `UPDATE orders_source_refreshes f SET
 state=CASE WHEN $4<>'' THEN 'FAILED' WHEN expires_at<=clock_timestamp() THEN 'PARTIAL'
 WHEN (SELECT count(DISTINCT c.stream) FROM orders_ingestion_pages c WHERE c.tenant_id=f.tenant_id AND c.moysklad_account=f.moysklad_account AND c.ozon_account=f.ozon_account AND c.stream IN('ozon-orders-discovery','ozon-orders-unfulfilled') AND c.finished_at>=f.requested_at AND (c.checkpoint->>'complete')::boolean AND COALESCE(jsonb_array_length(c.checkpoint->'failures'),0)=0 AND COALESCE(jsonb_array_length(c.checkpoint->'page_failures'),0)=0)=2 THEN 'SUCCEEDED' ELSE 'READING' END,
 code=CASE WHEN $4<>'' THEN $4 WHEN expires_at<=clock_timestamp() THEN 'READ_DEADLINE' ELSE '' END,
 postings_count=(SELECT count(*) FROM orders_posting_heads h WHERE h.tenant_id=f.tenant_id AND h.moysklad_account=f.moysklad_account AND h.ozon_account=f.ozon_account)
 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND state IN('QUEUED','READING')`, args...)
	if err != nil {
		return ordersError("record order source progress", err)
	}
	return nil
}
