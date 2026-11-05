package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
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
   LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(original.preview->'decisions')='array' THEN original.preview->'decisions' ELSE '[]'::jsonb END) decision
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
	if err = enrichOrderSplitTransfers(ctx, q, result, dependencies); err != nil {
		return err
	}
	return enrichOwnedAncestorSettlements(ctx, q, result, numbers)
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
	return finishUnmirroredSplitReserveRead(ctx, q, result, dependencies, rows)
}

const unmirroredUnassembledCandidatesSQL = `SELECT DISTINCT ON(split.id) split.id,split.work,
 decision->'posting',decision->'correlation',
 jsonb_set(parent_source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],'{evidence,checked_at}',to_jsonb(parent.checked_at)),
 COALESCE((SELECT jsonb_agg(jsonb_set(o.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],'{evidence,checked_at}',to_jsonb(h.checked_at)) ORDER BY h.posting_number)
 FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id
  AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number
 WHERE h.tenant_id=split.tenant_id AND h.moysklad_account=split.moysklad_account AND h.ozon_account=split.ozon_account
  AND NOT h.quarantined AND h.checked_at BETWEEN clock_timestamp()-interval '30 seconds' AND clock_timestamp()+interval '5 seconds'
  AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number),'[]'::jsonb),
 (NOT EXISTS(SELECT 1 FROM orders_projection_bindings binding WHERE binding.tenant_id=split.tenant_id
   AND binding.moysklad_account=split.moysklad_account AND binding.ozon_account=split.ozon_account
   AND binding.posting_number=split.posting_number AND binding.customer_order_id IS NOT NULL)
  AND NOT EXISTS(SELECT 1 FROM orders_work native WHERE native.tenant_id=split.tenant_id
   AND native.moysklad_account=split.moysklad_account AND native.ozon_account=split.ozon_account
   AND native.posting_number=split.posting_number
   AND native.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')))
FROM unnest($4::text[],$5::text[]) selected(number,split_id)
JOIN orders_work split ON split.id=selected.split_id AND split.tenant_id=$1 AND split.moysklad_account=$2 AND split.ozon_account=$3
 AND split.action='posting_split_unassembled' AND split.state='RECONCILED' AND split.dispatch_count=1 AND split.posting_number<>selected.number
JOIN orders_previews preview ON preview.id=split.work->>'preview_id' AND preview.tenant_id=split.tenant_id
 AND preview.moysklad_account=split.moysklad_account AND preview.ozon_account=split.ozon_account
CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(preview.preview->'decisions')='array' THEN preview.preview->'decisions' ELSE '[]'::jsonb END) decision
JOIN orders_posting_heads parent ON parent.tenant_id=split.tenant_id AND parent.moysklad_account=split.moysklad_account
 AND parent.ozon_account=split.ozon_account AND parent.posting_number=split.posting_number AND NOT parent.quarantined
JOIN orders_posting_observations parent_source ON parent_source.id=parent.observation_id AND parent_source.tenant_id=parent.tenant_id
 AND parent_source.moysklad_account=parent.moysklad_account AND parent_source.ozon_account=parent.ozon_account AND parent_source.posting_number=parent.posting_number
WHERE decision#>>'{posting,posting_number}'=split.posting_number
 AND decision#>>'{posting,evidence,revision}'=split.work#>>'{command,expected_source_revision}'
 AND parent.checked_at BETWEEN clock_timestamp()-interval '30 seconds' AND clock_timestamp()+interval '5 seconds'
 AND NOT EXISTS(SELECT 1 FROM orders_work ancestor WHERE ancestor.tenant_id=split.tenant_id
  AND ancestor.moysklad_account=split.moysklad_account AND ancestor.ozon_account=split.ozon_account
  AND ancestor.action IN ('posting_split','posting_split_unassembled') AND ancestor.dispatch_count=1
  AND ancestor.state NOT IN ('REJECTED','BLOCKED') AND ancestor.id<>split.id
  AND ((ancestor.posting_number=split.posting_number AND ancestor.created_at>split.created_at)
   OR (ancestor.posting_number<>split.posting_number AND
    (COALESCE(ancestor.work#>'{receipt,posting_numbers}','[]'::jsonb) ? split.posting_number
     OR (ancestor.state<>'RECONCILED' AND ancestor.work#>>'{command,commercial_order_id}'=split.work#>>'{command,commercial_order_id}')))))
ORDER BY split.id,selected.number LIMIT 10001`

func finishUnmirroredSplitReserveRead(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string, rows pgx.Rows) (map[string]string, error) {
	if err := rows.Err(); err != nil {
		return nil, ordersRowsError("read split reserve dependencies", err)
	}
	rows.Close()
	if err := enrichUnmirroredUnassembledReserves(ctx, q, result, dependencies); err != nil {
		return nil, err
	}
	if err := enrichNestedSplitSettlements(ctx, q, result, dependencies); err != nil {
		return nil, err
	}
	return dependencies, nil
}

func enrichUnmirroredUnassembledReserves(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string) error {
	if len(dependencies) == 0 {
		return nil
	}
	numbers := make([]string, 0, len(dependencies))
	for number := range dependencies {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	ids := make([]string, len(numbers))
	for i, number := range numbers {
		ids[i] = dependencies[number]
	}
	candidates, err := readUnmirroredUnassembledCandidates(ctx, q, result.Config.Scope, numbers, ids)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if !candidate.facts.NoParentNative {
			continue
		}
		if err = hydrateHistoricalSplitWork(ctx, q, result.Config.Scope, &candidate.facts.Split); err != nil {
			if historicalSplitUnavailable(err) {
				continue
			}
			return err
		}
		applyUnmirroredUnassembledCandidate(result, dependencies, candidate)

	}
	return nil
}

type unmirroredUnassembledCandidate struct {
	facts orders.UnmirroredUnassembledSplitFacts
}

func readUnmirroredUnassembledCandidates(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers, ids []string) ([]unmirroredUnassembledCandidate, error) {
	rows, err := q.Query(ctx, unmirroredUnassembledCandidatesSQL, append(ordersScopeArgs(scope), numbers, ids)...)
	if err != nil {
		return nil, ordersError("read unmirrored split proof", err)
	}
	defer rows.Close()
	result := make([]unmirroredUnassembledCandidate, 0, len(numbers))
	for rows.Next() {
		candidate, e := decodeUnmirroredUnassembledCandidate(rows)
		if e != nil {
			if historicalSplitUnavailable(e) {
				continue
			}
			return nil, e
		}
		result = append(result, candidate)
	}
	return result, ordersRowsError("finish unmirrored split proof", rows.Err())
}

func decodeUnmirroredUnassembledCandidate(row pgx.Row) (unmirroredUnassembledCandidate, error) {
	var result unmirroredUnassembledCandidate
	var id string
	var work, before, correlation, parent, children []byte
	if err := row.Scan(&id, &work, &before, &correlation, &parent, &children, &result.facts.NoParentNative); err != nil {
		return result, ordersError("decode unmirrored split proof", err)
	}
	for _, pair := range []struct {
		raw []byte
		out any
	}{{work, &result.facts.Split}, {before, &result.facts.Before}, {correlation, &result.facts.OriginalCorrelation}, {parent, &result.facts.Parent}, {children, &result.facts.Children}} {
		if ordersDecode(pair.raw, pair.out) != nil {
			return result, orders.ErrStale
		}
	}
	if result.facts.Split.ID != id {
		return result, orders.ErrStale
	}
	return result, nil
}

func applyUnmirroredUnassembledCandidate(result *orders.ReviewContext, dependencies map[string]string, candidate unmirroredUnassembledCandidate) {
	for _, child := range candidate.facts.Children {
		if dependencies[child.PostingNumber] != candidate.facts.Split.ID {
			continue
		}
		proof, err := orders.NewUnmirroredUnassembledSplitReserve(candidate.facts, child)
		if err != nil {
			continue
		}
		result.SplitReservePermissions[child.PostingNumber] = proof
		delete(result.SplitReserveBlocked, child.PostingNumber)
		delete(dependencies, child.PostingNumber)
	}
}
