package postgres

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// A first native root creation after confirmed split is a different proof from
// releasing a pre-existing whole-order reserve. Every chronology value is DB
// recorded. ANY other mirror history, including UNKNOWN and rejected work,
// invalidates this deliberately narrow initial admission.
const retainedSplitAdmissionSQL = `SELECT DISTINCT ON(split.id) split.id,split.work,
 original_decision->'posting',original_decision->'correlation',
 jsonb_set(parent_source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],'{evidence,checked_at}',to_jsonb(parent.checked_at)),
 COALESCE((SELECT jsonb_agg(jsonb_set(o.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],'{evidence,checked_at}',to_jsonb(h.checked_at)) ORDER BY h.posting_number)
 FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id
 AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number
 WHERE h.tenant_id=split.tenant_id AND h.moysklad_account=split.moysklad_account AND h.ozon_account=split.ozon_account
 AND NOT h.quarantined AND h.checked_at BETWEEN clock_timestamp()-interval '30 seconds' AND clock_timestamp()+interval '5 seconds'
 AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number),'[]'::jsonb),
 admission.work,native_decision->'posting',native_result.observed_target,
 split_result.created_at,admission.created_at,admission.dispatched_at,native_result.created_at
FROM unnest($4::text[],$5::text[]) selected(number,split_id)
JOIN orders_work split ON split.id=selected.split_id AND split.tenant_id=$1 AND split.moysklad_account=$2 AND split.ozon_account=$3
 AND split.action='posting_split_unassembled' AND split.state='RECONCILED' AND split.dispatch_count=1
 AND split.posting_number<>selected.number AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? split.posting_number
JOIN orders_reconciliations split_result ON split_result.work_id=split.id AND split_result.tenant_id=split.tenant_id
 AND split_result.moysklad_account=split.moysklad_account AND split_result.ozon_account=split.ozon_account
JOIN orders_previews original ON original.id=split.work->>'preview_id' AND original.tenant_id=split.tenant_id
 AND original.moysklad_account=split.moysklad_account AND original.ozon_account=split.ozon_account
CROSS JOIN LATERAL jsonb_array_elements(original.preview->'decisions') original_decision
JOIN orders_posting_heads parent ON parent.tenant_id=split.tenant_id AND parent.moysklad_account=split.moysklad_account
 AND parent.ozon_account=split.ozon_account AND parent.posting_number=split.posting_number AND NOT parent.quarantined
JOIN orders_posting_observations parent_source ON parent_source.id=parent.observation_id AND parent_source.tenant_id=parent.tenant_id
 AND parent_source.moysklad_account=parent.moysklad_account AND parent_source.ozon_account=parent.ozon_account AND parent_source.posting_number=parent.posting_number
JOIN orders_work admission ON admission.tenant_id=split.tenant_id AND admission.moysklad_account=split.moysklad_account
 AND admission.ozon_account=split.ozon_account AND admission.posting_number=split.posting_number
 AND admission.action='mirror_create' AND admission.state='RECONCILED' AND admission.dispatch_count=1
 AND admission.created_at>=split_result.created_at
JOIN orders_reconciliations native_result ON native_result.work_id=admission.id AND native_result.tenant_id=admission.tenant_id
 AND native_result.moysklad_account=admission.moysklad_account AND native_result.ozon_account=admission.ozon_account AND native_result.observed_target IS NOT NULL
JOIN orders_previews native_preview ON native_preview.id=admission.work->>'preview_id' AND native_preview.tenant_id=admission.tenant_id
 AND native_preview.moysklad_account=admission.moysklad_account AND native_preview.ozon_account=admission.ozon_account
CROSS JOIN LATERAL jsonb_array_elements(native_preview.preview->'decisions') native_decision
JOIN orders_projection_bindings binding ON binding.tenant_id=admission.tenant_id AND binding.moysklad_account=admission.moysklad_account
 AND binding.ozon_account=admission.ozon_account AND binding.posting_number=admission.posting_number
 AND binding.customer_order_id=native_result.correlation->>'customer_order_id'
WHERE original_decision#>>'{posting,posting_number}'=split.posting_number
 AND original_decision#>>'{posting,evidence,revision}'=split.work#>>'{command,expected_source_revision}'
 AND COALESCE(original_decision#>>'{correlation,customer_order_id}','')=''
 AND native_decision#>>'{posting,posting_number}'=admission.posting_number
 AND native_decision#>>'{posting,evidence,revision}'=admission.work#>>'{command,expected_source_revision}'
 AND parent.checked_at BETWEEN clock_timestamp()-interval '30 seconds' AND clock_timestamp()+interval '5 seconds'
 AND NOT EXISTS(SELECT 1 FROM orders_work other WHERE other.tenant_id=admission.tenant_id
 AND other.moysklad_account=admission.moysklad_account AND other.ozon_account=admission.ozon_account
 AND other.posting_number=admission.posting_number AND other.id<>admission.id
 AND other.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update'))
 AND NOT EXISTS(SELECT 1 FROM orders_target_fences fence WHERE fence.tenant_id=split.tenant_id
 AND fence.moysklad_account=split.moysklad_account AND fence.ozon_account=split.ozon_account AND fence.posting_number=split.posting_number)
 AND NOT EXISTS(SELECT 1 FROM orders_work ancestor WHERE ancestor.tenant_id=split.tenant_id
 AND ancestor.moysklad_account=split.moysklad_account AND ancestor.ozon_account=split.ozon_account
 AND ancestor.action IN ('posting_split','posting_split_unassembled') AND ancestor.dispatch_count=1
 AND ancestor.state NOT IN ('REJECTED','BLOCKED') AND ancestor.id<>split.id
 AND ((ancestor.posting_number=split.posting_number AND ancestor.created_at>split.created_at)
 OR (ancestor.posting_number<>split.posting_number AND (COALESCE(ancestor.work#>'{receipt,posting_numbers}','[]'::jsonb) ? split.posting_number
 OR (ancestor.state<>'RECONCILED' AND ancestor.work#>>'{command,commercial_order_id}'=split.work#>>'{command,commercial_order_id}')))))
ORDER BY split.id,selected.number LIMIT 10001`

func enrichRetainedSplitAdmissions(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string) error {
	if len(dependencies) == 0 {
		return nil
	}
	candidates, err := readRetainedSplitAdmissions(ctx, q, result.Config.Scope, dependencies)
	if err != nil {
		return err
	}
	if result.SplitReserveTransfers == nil {
		result.SplitReserveTransfers = make(map[string]orders.SplitReserveTransferPermission)
	}
	for _, facts := range candidates {
		if err = hydrateHistoricalSplitWork(ctx, q, result.Config.Scope, &facts.Split); err != nil {
			if historicalSplitUnavailable(err) {
				continue
			}
			return err
		}
		if !retainedAdmissionMappingsCurrent(facts, result) {
			continue
		}
		for _, child := range facts.Children {
			if dependencies[child.PostingNumber] != facts.Split.ID {
				continue
			}
			facts.Child = child
			proof, e := orders.NewRetainedSplitAdmission(facts)
			if e != nil {
				continue
			}
			if _, existing := result.SplitReserveTransfers[child.PostingNumber]; existing {
				continue
			}
			result.SplitReserveTransfers[child.PostingNumber] = proof
			delete(result.SplitReserveBlocked, child.PostingNumber)
		}
	}
	return nil
}

func retainedAdmissionMappingsCurrent(f orders.RetainedSplitAdmissionFacts, current *orders.ReviewContext) bool {
	c := f.Admission.Command
	if c.CustomerOrder == nil || c.CustomerOrder.OrganizationID != current.Config.OrganizationID || c.CustomerOrder.StoreID != c.Warehouse.MoyskladStoreID {
		return false
	}
	if !slices.ContainsFunc(current.WarehouseMappings, func(actual orders.WarehouseMappingRef) bool { return orderWarehouseMappingSame(c.Warehouse, actual) }) {
		return false
	}
	for _, position := range c.CustomerOrder.Positions {
		if !orderProductMappingPresent(position.Mapping, current.ProductMappings) {
			return false
		}
	}
	return true
}

func readRetainedSplitAdmissions(ctx context.Context, q ordersQuerier, scope orders.Scope, dependencies map[string]string) ([]orders.RetainedSplitAdmissionFacts, error) {
	numbers := make([]string, 0, len(dependencies))
	for number := range dependencies {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	ids := make([]string, len(numbers))
	for i, number := range numbers {
		ids[i] = dependencies[number]
	}
	rows, err := q.Query(ctx, retainedSplitAdmissionSQL, append(ordersScopeArgs(scope), numbers, ids)...)
	if err != nil {
		return nil, ordersError("read retained root native admissions", err)
	}
	defer rows.Close()
	result := make([]orders.RetainedSplitAdmissionFacts, 0, len(numbers))
	for rows.Next() {
		facts, e := decodeRetainedSplitAdmission(rows)
		if e != nil {
			if historicalSplitUnavailable(e) {
				continue
			}
			return nil, e
		}
		result = append(result, facts)
	}
	return result, ordersRowsError("finish retained root admissions", rows.Err())
}

func decodeRetainedSplitAdmission(row pgx.Row) (orders.RetainedSplitAdmissionFacts, error) {
	var f orders.RetainedSplitAdmissionFacts
	var id string
	var split, before, correlation, parent, children, admission, source, target []byte
	if err := row.Scan(&id, &split, &before, &correlation, &parent, &children, &admission, &source, &target, &f.SplitReconciledAt, &f.NativeCreatedAt, &f.NativeDispatchedAt, &f.NativeReconciledAt); err != nil {
		return f, ordersError("decode retained root admission", err)
	}
	for _, pair := range []struct {
		raw []byte
		out any
	}{{split, &f.Split}, {before, &f.Before}, {correlation, &f.OriginalCorrelation}, {parent, &f.Parent}, {children, &f.Children}, {admission, &f.Admission}, {source, &f.AdmissionSource}, {target, &f.Target}} {
		if ordersDecode(pair.raw, pair.out) != nil {
			return f, orders.ErrStale
		}
	}
	if id != f.Split.ID {
		return f, orders.ErrStale
	}
	f.SoleNativeAdmission = true // Established by the scoped, immutable work query above.
	return f, nil
}
