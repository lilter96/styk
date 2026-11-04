package postgres

import (
	"context"
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

// A locally acknowledged split can identify children even when their provider
// details omit parent_posting_number. Admission of those details is not proof
// that the original native reserve has already been transferred.
const splitReserveBlocksSQL = `SELECT h.posting_number FROM orders_posting_heads h
JOIN orders_posting_observations source ON source.id=h.observation_id
 AND source.tenant_id=h.tenant_id AND source.moysklad_account=h.moysklad_account
 AND source.ozon_account=h.ozon_account AND source.posting_number=h.posting_number
JOIN orders_posting_identities identity ON identity.tenant_id=h.tenant_id
 AND identity.ozon_account=h.ozon_account AND identity.posting_number=h.posting_number
WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3
 AND h.posting_number=ANY($4::text[]) AND EXISTS (
 SELECT 1 FROM orders_work split WHERE split.tenant_id=h.tenant_id
 AND split.moysklad_account=h.moysklad_account AND split.ozon_account=h.ozon_account
 AND split.action='posting_split' AND split.dispatch_count=1
 AND split.state NOT IN ('REJECTED','BLOCKED') AND (
  (split.posting_number<>h.posting_number AND (
   COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number
   OR (split.state<>'RECONCILED' AND identity.created_at>=split.created_at
    AND source.commercial_order_id=split.work#>>'{command,commercial_order_id}')))
  OR (split.posting_number=h.posting_number AND split.state='RECONCILED'
   AND NOT (COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number))
 )) ORDER BY h.posting_number LIMIT 10001`

func enrichOrderSplitReserveBlocks(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	if len(numbers) == 0 || !slices.ContainsFunc(result.Config.FieldMappings, func(m orders.FieldMapping) bool {
		return m.Target == "positions.reserve" && m.Source == orders.ReserveOpenQuantitySource
	}) {
		return nil
	}
	rows, err := q.Query(ctx, splitReserveBlocksSQL, append(ordersScopeArgs(result.Config.Scope), numbers)...)
	if err != nil {
		return ordersError("read split reserve dependencies", err)
	}
	defer rows.Close()
	result.SplitReserveBlocked = make(map[string]bool, len(numbers))
	for rows.Next() {
		var number string
		if err = rows.Scan(&number); err != nil {
			return ordersError("decode split reserve dependency", err)
		}
		result.SplitReserveBlocked[number] = true
	}
	return ordersRowsError("read split reserve dependencies", rows.Err())
}
