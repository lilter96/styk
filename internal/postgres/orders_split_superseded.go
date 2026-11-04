package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// Frozen original decisions and acknowledged child membership establish the
// local split identity. A related-postings group or HTTP 404 is never used here.
// Current complete child heads are fetched in the same scoped database query.
const supersededSplitReserveSQL = `SELECT split.work,decision->'posting',decision->'correlation',
 COALESCE((SELECT jsonb_agg(jsonb_set(observed.posting,'{evidence,checked_at}',to_jsonb(child.checked_at)) ORDER BY child.posting_number)
  FROM orders_posting_heads child JOIN orders_posting_observations observed
   ON observed.id=child.observation_id AND observed.tenant_id=child.tenant_id
   AND observed.moysklad_account=child.moysklad_account AND observed.ozon_account=child.ozon_account
   AND observed.posting_number=child.posting_number
  WHERE child.tenant_id=split.tenant_id
   AND child.moysklad_account=split.moysklad_account AND child.ozon_account=split.ozon_account
   AND NOT child.quarantined
   AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? child.posting_number),'[]'::jsonb)
FROM orders_posting_heads h
JOIN orders_posting_observations source ON source.id=h.observation_id
 AND source.tenant_id=h.tenant_id AND source.moysklad_account=h.moysklad_account
 AND source.ozon_account=h.ozon_account AND source.posting_number=h.posting_number
JOIN LATERAL (SELECT w.* FROM orders_work w
 WHERE w.tenant_id=h.tenant_id AND w.moysklad_account=h.moysklad_account
  AND w.ozon_account=h.ozon_account AND w.posting_number=h.posting_number
  AND w.action IN ('posting_split','posting_split_unassembled') AND w.dispatch_count=1 AND w.state NOT IN ('REJECTED','BLOCKED')
 ORDER BY w.created_at DESC,w.id DESC LIMIT 1) split ON split.state='RECONCILED'
JOIN orders_previews original ON original.id=split.work->>'preview_id'
 AND original.tenant_id=split.tenant_id AND original.moysklad_account=split.moysklad_account
 AND original.ozon_account=split.ozon_account
CROSS JOIN LATERAL jsonb_array_elements(original.preview->'decisions') decision
WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3
 AND h.posting_number=ANY($4::text[]) AND NOT h.quarantined
 AND source.posting#>>'{status,value}'='cancelled_from_split_pending'
 AND decision#>>'{posting,posting_number}'=split.posting_number
 AND decision#>>'{posting,evidence,revision}'=split.work#>>'{command,expected_source_revision}'
 AND NOT (COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number)
 AND NOT EXISTS (SELECT 1 FROM orders_work ancestor
  WHERE ancestor.tenant_id=split.tenant_id AND ancestor.moysklad_account=split.moysklad_account
   AND ancestor.ozon_account=split.ozon_account AND ancestor.action IN ('posting_split','posting_split_unassembled')
   AND ancestor.dispatch_count=1 AND ancestor.state NOT IN ('REJECTED','BLOCKED')
   AND ancestor.posting_number<>split.posting_number
   AND (COALESCE(ancestor.work#>'{receipt,posting_numbers}','[]'::jsonb) ? split.posting_number
    OR (ancestor.state<>'RECONCILED' AND ancestor.work#>>'{command,commercial_order_id}'=split.work#>>'{command,commercial_order_id}')))
ORDER BY h.posting_number LIMIT 10001`

func enrichOrderSupersededSplitReserves(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	rows, err := q.Query(ctx, supersededSplitReserveSQL, append(ordersScopeArgs(result.Config.Scope), numbers)...)
	if err != nil {
		return ordersError("read superseded split reserve facts", err)
	}
	defer rows.Close()
	result.SplitSupersededReserves = make(map[string]orders.SplitSupersededReserveFacts)
	parents := make(map[string]orders.Posting, len(result.Postings))
	for _, parent := range result.Postings {
		parents[parent.PostingNumber] = parent
	}
	for rows.Next() {
		facts, e := decodeSupersededSplitCandidate(rows)
		if e != nil {
			return e
		}
		facts.Parent = parents[facts.Before.PostingNumber]
		facts.Baseline = result.PositionBaselines[facts.Before.PostingNumber]
		result.SplitSupersededReserves[facts.Before.PostingNumber] = facts
	}
	if err = ordersRowsError("read superseded split reserve facts", rows.Err()); err != nil {
		return err
	}
	rows.Close()
	return hydrateSupersededSplitHistory(ctx, q, result)
}
