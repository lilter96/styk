package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// RequestOrderCheck only advances a currently enabled schedule. It does not
// override paused configuration or authorize a provider write.
func (s *Store) RequestOrderCheck(ctx context.Context, scope orders.Scope, actor string) error {
	if scope.Validate() != nil {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin order check request", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, scope.Tenant, actor); err != nil {
		return err
	}
	args := ordersScopeArgs(scope)
	result, err := tx.Exec(ctx, `UPDATE orders_configs SET next_run_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND enabled`, args...)
	if err != nil {
		return ordersError("request order check", err)
	}
	if result.RowsAffected() != 1 {
		return orders.ErrBlocked
	}
	return ordersRowsError("commit order check request", tx.Commit(ctx))
}
