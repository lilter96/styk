package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// This is an explicit presence/absence traversal, not native ownership. The
// separate full event traversal validates all states and historical frontiers.
const ownedForestNativeHistorySQL = `SELECT p.number,
 ARRAY(SELECT b.customer_order_id FROM orders_projection_bindings b
 WHERE b.tenant_id=$1 AND b.moysklad_account=$2 AND b.ozon_account=$3
 AND b.posting_number=p.number AND b.customer_order_id IS NOT NULL),
 ARRAY(SELECT w.id FROM orders_work w
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND w.posting_number=p.number
 AND w.action IN('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
 AND NOT COALESCE((` + ownedForestUnsentSelfSQL + `),false) LIMIT 1)
 FROM unnest($4::text[]) p(number) ORDER BY p.number`

func readOwnedForestNativeHistory(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string, selfID, fingerprint, selected, node, split string) ([]orders.NestedSplitNativeHistory, error) {
	unsentSelf, err := readOwnedForestUnsentSelf(ctx, q, scope, selfID, fingerprint, selected, node, split)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, ownedForestNativeHistorySQL, append(ordersScopeArgs(scope), numbers, selfID, fingerprint, selected, node, split, unsentSelf)...)
	if err != nil {
		return nil, ordersError("read forest native absence witnesses", err)
	}
	defer rows.Close()
	history := make([]orders.NestedSplitNativeHistory, 0, len(numbers))
	for rows.Next() {
		item := orders.NestedSplitNativeHistory{Scope: scope}
		if err = rows.Scan(&item.PostingNumber, &item.BindingIDs, &item.MirrorWorkIDs); err != nil {
			return nil, ordersRowsError("decode forest native absence witness", err)
		}
		history = append(history, item)
	}
	if err = rows.Err(); err != nil {
		return nil, ordersRowsError("finish forest native absence witnesses", err)
	}
	if len(history) != len(numbers) {
		return nil, errDeferredOwnedUnsafe
	}
	return history, nil
}
