package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// A retained package may reduce its own existing reserve before children are
// admitted. This proof never authorizes a child's reserve or infers ancestry
// from matching products. The frozen pre-split review binds the native owner.
const retainedSplitReserveSQL = `SELECT h.posting_number,split.id,split.state,split.action,
 decision->'posting',decision->'correlation'
FROM orders_posting_heads h
JOIN LATERAL (SELECT w.* FROM orders_work w
 WHERE w.tenant_id=h.tenant_id AND w.moysklad_account=h.moysklad_account
  AND w.ozon_account=h.ozon_account AND w.posting_number=h.posting_number
  AND w.action IN ('posting_split','posting_split_unassembled') AND w.dispatch_count=1
  AND w.state NOT IN ('REJECTED','BLOCKED')
 ORDER BY w.created_at DESC,w.id DESC LIMIT 1) split ON true
JOIN orders_previews original ON original.id=split.work->>'preview_id'
 AND original.tenant_id=split.tenant_id AND original.moysklad_account=split.moysklad_account
 AND original.ozon_account=split.ozon_account
CROSS JOIN LATERAL jsonb_array_elements(original.preview->'decisions') decision
WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3
 AND h.posting_number=ANY($4::text[])
 AND decision#>>'{posting,posting_number}'=split.posting_number
 AND decision#>>'{posting,evidence,revision}'=split.work#>>'{command,expected_source_revision}'
 AND decision#>>'{posting,parent_posting_number,presence}' IN ('missing','null')
 AND COALESCE(decision#>>'{correlation,customer_order_id}','')<>''
 AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number
 AND NOT EXISTS (SELECT 1 FROM orders_work ancestor
  JOIN orders_posting_identities identity ON identity.tenant_id=ancestor.tenant_id
   AND identity.ozon_account=ancestor.ozon_account AND identity.posting_number=split.posting_number
  WHERE ancestor.tenant_id=split.tenant_id AND ancestor.moysklad_account=split.moysklad_account
   AND ancestor.ozon_account=split.ozon_account AND ancestor.action IN ('posting_split','posting_split_unassembled')
   AND ancestor.dispatch_count=1 AND ancestor.state NOT IN ('REJECTED','BLOCKED')
   AND ancestor.posting_number<>split.posting_number AND ancestor.dispatched_at<=split.dispatched_at
   AND (COALESCE(ancestor.work#>'{receipt,posting_numbers}','[]'::jsonb) ? split.posting_number
    OR (ancestor.state<>'RECONCILED' AND identity.created_at>=ancestor.dispatched_at
     AND ancestor.work#>>'{command,commercial_order_id}'=split.work#>>'{command,commercial_order_id}')))
ORDER BY h.posting_number LIMIT 10001`

func enrichOrderRetainedSplitReserves(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	rows, err := q.Query(ctx, retainedSplitReserveSQL, append(ordersScopeArgs(result.Config.Scope), numbers)...)
	if err != nil {
		return ordersError("read retained split reserve proofs", err)
	}
	defer rows.Close()
	result.SplitRetainedReserves = make(map[string]orders.SplitRetainedReservePermission, len(numbers))
	for rows.Next() {
		var number, state, action string
		var rawPosting, rawCorrelation []byte
		permission := orders.SplitRetainedReservePermission{Scope: result.Config.Scope}
		if err = rows.Scan(&number, &permission.SplitWorkID, &state, &action, &rawPosting, &rawCorrelation); err != nil {
			return ordersError("decode retained split reserve proof", err)
		}
		if state != string(orders.StateReconciled) || action != string(orders.ActionPostingSplit) {
			continue
		}
		if err = ordersDecode(rawPosting, &permission.Before); err != nil {
			return err
		}
		if err = ordersDecode(rawCorrelation, &permission.Correlation); err != nil {
			return err
		}
		result.SplitRetainedReserves[number] = permission
	}
	return ordersRowsError("read retained split reserve proofs", rows.Err())
}
