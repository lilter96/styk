package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// ReadOrderMetadataCreation authorizes a current merchant session without
// replaying the original request's actor-bound idempotency key. The returned
// intent never grants permission to create a native attribute.
func (s *Store) ReadOrderMetadataCreation(ctx context.Context, scope orders.Scope, code, actor string) (orders.MetadataCreationIntent, error) {
	if scope.Validate() != nil || !ordersRequestKey(code) || actor == "" {
		return orders.MetadataCreationIntent{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.MetadataCreationIntent{}, ordersError("begin metadata recovery read", err)
	}
	defer rollbackMerchantMutation(tx)
	owner, err := ordersActor(ctx, tx, scope.Tenant, actor)
	if err != nil {
		return orders.MetadataCreationIntent{}, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT intent FROM orders_metadata_creation_intents WHERE tenant_id=$1 AND moysklad_account=$2 AND unique_code=$3 AND owner_user_id=$4`, scope.Tenant, scope.MoyskladAccount, code, owner).Scan(&raw)
	if err != nil {
		return orders.MetadataCreationIntent{}, ordersError("read metadata recovery intent", err)
	}
	var intent orders.MetadataCreationIntent
	if err = ordersDecode(raw, &intent); err != nil {
		return intent, err
	}
	if intent.Scope != scope || intent.UniqueCode != code {
		return orders.MetadataCreationIntent{}, orders.ErrNotFound
	}
	intent.DispatchPermitted = false
	return intent, ordersRowsError("finish metadata recovery authorization", tx.Commit(ctx))
}
