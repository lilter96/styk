package postgres

import (
	"context"
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

// A locally acknowledged split can identify children even when their provider
// details omit parent_posting_number. Admission of those details is not proof
// that the original native reserve has already been transferred.
const splitReserveBlocksSQL = `SELECT h.posting_number,dependency.id,dependency.posting_number,dependency.unencumbered
FROM orders_posting_heads h
JOIN orders_posting_observations source ON source.id=h.observation_id
 AND source.tenant_id=h.tenant_id AND source.moysklad_account=h.moysklad_account
 AND source.ozon_account=h.ozon_account AND source.posting_number=h.posting_number
JOIN orders_posting_identities identity ON identity.tenant_id=h.tenant_id
 AND identity.ozon_account=h.ozon_account AND identity.posting_number=h.posting_number
JOIN LATERAL (
 SELECT split.id,split.posting_number,
 (split.action='posting_split' AND split.state='RECONCILED' AND split.posting_number<>h.posting_number
  AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number
  AND NOT EXISTS (SELECT 1 FROM orders_work parent_write
   WHERE parent_write.tenant_id=split.tenant_id AND parent_write.moysklad_account=split.moysklad_account
    AND parent_write.ozon_account=split.ozon_account AND parent_write.posting_number=split.posting_number
    AND parent_write.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
    AND parent_write.dispatch_count=1 AND parent_write.dispatched_at<=split.dispatched_at)
  AND EXISTS (SELECT 1 FROM orders_previews original,
   LATERAL jsonb_array_elements(original.preview->'decisions') decision
   WHERE original.id=split.work->>'preview_id' AND original.tenant_id=split.tenant_id
    AND original.moysklad_account=split.moysklad_account AND original.ozon_account=split.ozon_account
    AND decision#>>'{posting,posting_number}'=split.posting_number
    AND decision#>>'{posting,evidence,revision}'=split.work#>>'{command,expected_source_revision}'
    AND decision#>>'{posting,parent_posting_number,presence}' IN ('missing','null')
    AND COALESCE(decision#>>'{correlation,customer_order_id}','')='')
  AND NOT EXISTS (SELECT 1 FROM orders_work ancestor
   JOIN orders_posting_identities parent_identity ON parent_identity.tenant_id=ancestor.tenant_id
    AND parent_identity.ozon_account=ancestor.ozon_account AND parent_identity.posting_number=split.posting_number
   WHERE ancestor.tenant_id=split.tenant_id AND ancestor.moysklad_account=split.moysklad_account
    AND ancestor.ozon_account=split.ozon_account AND ancestor.action IN ('posting_split','posting_split_unassembled')
    AND ancestor.dispatch_count=1 AND ancestor.state NOT IN ('REJECTED','BLOCKED')
    AND ancestor.posting_number<>split.posting_number AND ancestor.dispatched_at<=split.dispatched_at
    AND (COALESCE(ancestor.work#>'{receipt,posting_numbers}','[]'::jsonb) ? split.posting_number
     OR (ancestor.state<>'RECONCILED' AND parent_identity.created_at>=ancestor.dispatched_at
      AND ancestor.work#>>'{command,commercial_order_id}'=split.work#>>'{command,commercial_order_id}')))
 ) AS unencumbered
 FROM orders_work split WHERE split.tenant_id=h.tenant_id
 AND split.moysklad_account=h.moysklad_account AND split.ozon_account=h.ozon_account
 AND split.action IN ('posting_split','posting_split_unassembled') AND split.dispatch_count=1
 AND split.state NOT IN ('REJECTED','BLOCKED') AND (
  (split.posting_number<>h.posting_number AND (
   COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number
   OR (split.state<>'RECONCILED' AND identity.created_at>=split.dispatched_at
    AND source.commercial_order_id=split.work#>>'{command,commercial_order_id}')))
  OR (split.posting_number=h.posting_number AND split.state='RECONCILED'
   AND NOT (COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number))
 ) ORDER BY split.created_at DESC,split.id DESC LIMIT 1
) dependency ON true
WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3
 AND h.posting_number=ANY($4::text[]) ORDER BY h.posting_number LIMIT 10001`

func enrichOrderSplitReserveBlocks(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	if err := enrichOrderSplitTransferBusy(ctx, q, result, numbers); err != nil {
		return err
	}
	if len(numbers) == 0 || !slices.ContainsFunc(result.Config.FieldMappings, func(m orders.FieldMapping) bool {
		return m.Target == "positions.reserve" && m.Source == orders.ReserveOpenQuantitySource
	}) {
		return nil
	}
	dependencies, err := readOrderSplitReserveBlocks(ctx, q, result, numbers)
	if err != nil {
		return err
	}
	if err = enrichOrderRetainedSplitReserves(ctx, q, result, numbers); err != nil {
		return err
	}
	if err = enrichOrderSupersededSplitReserves(ctx, q, result, numbers); err != nil {
		return err
	}
	return enrichOrderSplitTransfers(ctx, q, result, dependencies)
}

func readOrderSplitReserveBlocks(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) (map[string]string, error) {
	rows, err := q.Query(ctx, splitReserveBlocksSQL, append(ordersScopeArgs(result.Config.Scope), numbers)...)
	if err != nil {
		return nil, ordersError("read split reserve dependencies", err)
	}
	defer rows.Close()
	result.SplitReserveBlocked = make(map[string]bool, len(numbers))
	result.SplitReservePermissions = make(map[string]orders.SplitReservePermission, len(numbers))
	dependencies := make(map[string]string, len(numbers))
	for rows.Next() {
		var number, workID, parent string
		var unencumbered bool
		if err = rows.Scan(&number, &workID, &parent, &unencumbered); err != nil {
			return nil, ordersError("decode split reserve dependency", err)
		}
		if unencumbered {
			result.SplitReservePermissions[number] = orders.SplitReservePermission{Scope: result.Config.Scope, SplitWorkID: workID, ParentPostingNumber: parent, PostingNumber: number}
		} else {
			result.SplitReserveBlocked[number] = true
			dependencies[number] = workID
		}
	}
	return dependencies, ordersRowsError("read split reserve dependencies", rows.Err())
}
