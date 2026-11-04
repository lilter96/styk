package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// A pause revokes new mutations, not observation of a request already sent.
// Reads retain the exact original credential scope and active operation owner;
// they cannot begin dispatch or renew an expired automation grant.
func orderReadbackCurrent(ctx context.Context, tx pgx.Tx, work orders.Work) (bool, error) {
	if !work.State.ReadOnly() || work.DispatchCount != 1 {
		return false, nil
	}
	var owner string
	err := tx.QueryRow(ctx, `SELECT owner_user_id FROM orders_operations WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4`, work.OperationID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount).Scan(&owner)
	if err != nil {
		return false, ordersError("read original reconciliation owner", err)
	}
	if err = ordersOwnerCurrent(ctx, tx, work.Scope.Tenant, owner); err != nil {
		return false, err
	}
	revisions, err := ordersCredentials(ctx, tx, work.Scope)
	if err != nil || revisions != work.Credentials {
		return false, err
	}
	var connected bool
	err = tx.QueryRow(ctx, `SELECT count(*)=2 FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, ordersScopeArgs(work.Scope)...).Scan(&connected)
	return connected, ordersRowsError("check reconciliation account ownership", err)
}
