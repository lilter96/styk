package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Provider reads follow the current recorded account permission, not the browser
// session that originally configured it. Revocation is checked under DB locks.
func validateOrderReadAuthority(ctx context.Context, tx pgx.Tx, config orders.Config) error {
	if !config.ReadsEnabled() || config.Scope.Validate() != nil || config.Revision < 1 {
		return orders.ErrBlocked
	}
	if config.ReadEnabled {
		return validateSourceReadOwner(ctx, tx, config.Scope)
	}
	var owner string
	args := append(ordersScopeArgs(config.Scope), config.Revision)
	err := tx.QueryRow(ctx, `SELECT r.owner_user_id FROM orders_config_revisions r
 JOIN orders_configs c USING(tenant_id,moysklad_account,ozon_account,revision)
 WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3
 AND r.revision=$4 AND c.enabled FOR SHARE OF c,r`, args...).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrBlocked
	}
	if err != nil {
		return ordersError("read recorded order read permission owner", err)
	}
	if err = ordersOwnerCurrent(ctx, tx, config.Scope.Tenant, owner); err != nil {
		return err
	}
	if config.Automation != nil {
		_, err = ordersGrantOwner(ctx, tx, config, orderAutomationActor(*config.Automation))
	}
	return err
}
