package postgres

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) DueCommissionConfigs(ctx context.Context, _ time.Time, limit int) ([]orders.Config, error) {
	if limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ordersError("begin due commission policies", err)
	}
	defer rollbackMerchantMutation(tx)
	rows, err := tx.Query(ctx, `SELECT r.config,COALESCE(p.enabled,false),p.tenant_id IS NOT NULL FROM orders_finance_schedule n JOIN orders_configs c USING(tenant_id,moysklad_account,ozon_account) JOIN orders_config_revisions r USING(tenant_id,moysklad_account,ozon_account,revision) LEFT JOIN orders_source_permissions p USING(tenant_id,moysklad_account,ozon_account) WHERE r.config#>>'{finance,enabled}'='true' AND r.config#>>'{finance,automatic}'='true' AND (p.enabled OR (p.tenant_id IS NULL AND c.enabled)) AND n.next_run_at<=clock_timestamp() ORDER BY n.next_run_at,n.tenant_id LIMIT $1 FOR UPDATE OF n SKIP LOCKED`, limit)
	if err != nil {
		return nil, ordersError("select due commission policies", err)
	}
	configs, err := scanOrderConfigs(rows)
	if err != nil {
		return nil, err
	}
	for _, config := range configs {
		if _, err = tx.Exec(ctx, `UPDATE orders_finance_schedule SET next_run_at=clock_timestamp()+interval '6 hours' WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, ordersScopeArgs(config.Scope)...); err != nil {
			return nil, ordersError("advance commission schedule", err)
		}
	}
	return configs, ordersRowsError("commit due commission policies", tx.Commit(ctx))
}
