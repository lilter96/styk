package postgres

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// The root creation remains the ownership anchor. Every intervening root mirror
// is returned as a bounded typed witness, including blocked and UNKNOWN work;
// none can disappear merely because it lacks reconciliation. Full native
// position comparisons happen in SQL without aggregating repeated large arrays.
const retainedAdmissionRefreshSQL = `SELECT DISTINCT ON(split.id) split.id,split.work,
 original_decision->'posting',original_decision->'correlation',
 jsonb_set(parent_source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],'{evidence,checked_at}',to_jsonb(parent.checked_at)),
 COALESCE((SELECT jsonb_agg(jsonb_set(o.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],'{evidence,checked_at}',to_jsonb(h.checked_at)) ORDER BY h.posting_number)
 FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id
 AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number
 WHERE h.tenant_id=split.tenant_id AND h.moysklad_account=split.moysklad_account AND h.ozon_account=split.ozon_account
 AND NOT h.quarantined AND h.checked_at BETWEEN clock_timestamp()-interval '30 seconds' AND clock_timestamp()+interval '5 seconds'
 AND COALESCE(split.work#>'{receipt,posting_numbers}','[]'::jsonb) ? h.posting_number),'[]'::jsonb),
 admission.work,native_decision->'posting',native_result.observed_target,
 split_result.created_at,admission.created_at,admission.dispatched_at,native_result.created_at,
 refresh.work,refresh_decision->'posting',refresh.observed_target,history.witness
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
JOIN LATERAL (SELECT w.*,r.observed_target FROM orders_work w
 JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id
 AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
 WHERE w.tenant_id=admission.tenant_id AND w.moysklad_account=admission.moysklad_account
 AND w.ozon_account=admission.ozon_account AND w.posting_number=admission.posting_number
 AND w.id<>admission.id AND w.action='mirror_update' AND w.state='RECONCILED' AND w.dispatch_count IN (0,1)
 AND r.observed_target IS NOT NULL ORDER BY w.created_at DESC,w.id DESC LIMIT 1) refresh ON true
JOIN orders_previews refresh_preview ON refresh_preview.id=refresh.work->>'preview_id'
 AND refresh_preview.tenant_id=refresh.tenant_id AND refresh_preview.moysklad_account=refresh.moysklad_account
 AND refresh_preview.ozon_account=refresh.ozon_account
CROSS JOIN LATERAL jsonb_array_elements(refresh_preview.preview->'decisions') refresh_decision
JOIN LATERAL (SELECT COALESCE(jsonb_agg(jsonb_build_object(
 'WorkID',h.id,'CommandID',h.work#>>'{command,id}','Fingerprint',h.work->>'command_fingerprint',
 'Scope',h.work->'scope','CommandScope',h.work#>'{command,scope}',
 'Action',h.action,'CommandAction',h.work#>>'{command,action}','State',h.state,'DispatchCount',h.dispatch_count,
 'PostingNumber',h.posting_number,'CommercialOrderID',h.work#>>'{command,commercial_order_id}',
 'Correlation',h.work#>'{command,correlation}','BaselineWorkID',h.work#>>'{command,position_baseline,work_id}',
 'BaselineCommandID',h.work#>>'{command,position_baseline,command_id}',
 'ReceiptState',h.work#>>'{receipt,state}','HTTPStatus',h.work#>'{receipt,http_status}',
 'ReceiptCommandID',h.work#>>'{receipt,command_id}','ExternalID',h.work#>>'{receipt,external_id}',
 'CreatedAt',h.created_at,'DispatchedAt',h.dispatched_at,'ReconciledAt',hr.created_at,
 'ObservedAt',hr.observed_target->'observed_at',
 'NoWriteIntent',NOT EXISTS(SELECT 1 FROM orders_write_intents wi WHERE wi.work_id=h.id
  AND wi.tenant_id=h.tenant_id AND wi.moysklad_account=h.moysklad_account AND wi.ozon_account=h.ozon_account),
 'PositionFrontierUnchanged',COALESCE(
  (SELECT jsonb_agg(p ORDER BY p->>'id') FROM jsonb_array_elements(CASE WHEN jsonb_typeof(h.work#>'{command,target_positions}')='array' THEN h.work#>'{command,target_positions}' ELSE '[]'::jsonb END) p)
   =(SELECT jsonb_agg(p ORDER BY p->>'id') FROM jsonb_array_elements(CASE WHEN jsonb_typeof(native_result.observed_target->'positions')='array' THEN native_result.observed_target->'positions' ELSE '[]'::jsonb END) p)
  AND (SELECT jsonb_agg(p ORDER BY p->>'id') FROM jsonb_array_elements(CASE WHEN jsonb_typeof(hr.observed_target->'positions')='array' THEN hr.observed_target->'positions' ELSE '[]'::jsonb END) p)
   =(SELECT jsonb_agg(p ORDER BY p->>'id') FROM jsonb_array_elements(CASE WHEN jsonb_typeof(native_result.observed_target->'positions')='array' THEN native_result.observed_target->'positions' ELSE '[]'::jsonb END) p),false),
 'NativeIdentityUnchanged',COALESCE(hr.observed_target->'scope'=native_result.observed_target->'scope'
 AND hr.observed_target->'correlation'=native_result.observed_target->'correlation'
 AND hr.observed_target->>'complete'='true'
 AND hr.observed_target#>>'{fields,applicable,presence}'='value'
 AND hr.observed_target#>>'{fields,applicable,value}'='true'
 AND COALESCE(hr.observed_target#>>'{source_deleted,presence}','missing') IN ('missing','null','empty','value')
 AND COALESCE(hr.observed_target#>>'{source_deleted,value}','')=''
 AND COALESCE(hr.observed_target#>>'{fields,organization_id}','')=COALESCE(native_result.observed_target#>>'{fields,organization_id}','')
 AND COALESCE(hr.observed_target#>>'{fields,agent_id}','')=COALESCE(native_result.observed_target#>>'{fields,agent_id}','')
 AND COALESCE(hr.observed_target#>>'{fields,store_id}','')=COALESCE(native_result.observed_target#>>'{fields,store_id}','')
 AND COALESCE(hr.observed_target#>>'{fields,sales_channel_id}','')=COALESCE(native_result.observed_target#>>'{fields,sales_channel_id}','')
 AND COALESCE(hr.observed_target#>>'{fields,currency_id}','')=COALESCE(native_result.observed_target#>>'{fields,currency_id}',''),false)
 ) ORDER BY h.created_at,h.id),'[]'::jsonb) AS witness
 FROM (SELECT w.* FROM orders_work w WHERE w.tenant_id=admission.tenant_id
 AND w.moysklad_account=admission.moysklad_account AND w.ozon_account=admission.ozon_account
 AND w.posting_number=admission.posting_number AND w.id<>admission.id
 AND w.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
 ORDER BY w.created_at,w.id LIMIT 1025) h
 LEFT JOIN orders_reconciliations hr ON hr.work_id=h.id AND hr.tenant_id=h.tenant_id
 AND hr.moysklad_account=h.moysklad_account AND hr.ozon_account=h.ozon_account) history ON true

WHERE original_decision#>>'{posting,posting_number}'=split.posting_number
 AND original_decision#>>'{posting,evidence,revision}'=split.work#>>'{command,expected_source_revision}'
 AND COALESCE(original_decision#>>'{correlation,customer_order_id}','')=''
 AND native_decision#>>'{posting,posting_number}'=admission.posting_number
 AND native_decision#>>'{posting,evidence,revision}'=admission.work#>>'{command,expected_source_revision}'
 AND refresh_decision#>>'{posting,posting_number}'=refresh.posting_number
 AND refresh_decision#>>'{posting,evidence,revision}'=refresh.work#>>'{command,expected_source_revision}'
 AND parent.checked_at BETWEEN clock_timestamp()-interval '30 seconds' AND clock_timestamp()+interval '5 seconds'

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

func enrichRetainedAdmissionRefreshes(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string) error {
	if len(dependencies) == 0 {
		return nil
	}
	candidates, err := readRetainedAdmissionRefreshes(ctx, q, result.Config.Scope, dependencies)
	if err != nil {
		return err
	}
	if result.SplitReserveTransfers == nil {
		result.SplitReserveTransfers = make(map[string]orders.SplitReserveTransferPermission)
	}
	for _, facts := range candidates {
		if err = hydrateHistoricalSplitWork(ctx, q, result.Config.Scope, &facts.Root.Split); err != nil {
			if historicalSplitUnavailable(err) {
				continue
			}
			return err
		}
		applyRetainedAdmissionRefresh(result, dependencies, facts)
	}
	return nil
}

func applyRetainedAdmissionRefresh(result *orders.ReviewContext, dependencies map[string]string, facts orders.RetainedSplitRefreshFacts) {
	if !retainedAdmissionMappingsCurrent(facts.Root, result) {
		return
	}
	for _, child := range facts.Root.Children {
		if dependencies[child.PostingNumber] != facts.Root.Split.ID {
			continue
		}
		if _, exists := result.SplitReserveTransfers[child.PostingNumber]; exists {
			continue
		}
		facts.Root.Child = child
		proof, err := orders.NewRetainedSplitAdmissionRefresh(facts)
		if err != nil {
			continue
		}
		result.SplitReserveTransfers[child.PostingNumber] = proof
		delete(result.SplitReserveBlocked, child.PostingNumber)
	}
}

func readRetainedAdmissionRefreshes(ctx context.Context, q ordersQuerier, scope orders.Scope, dependencies map[string]string) ([]orders.RetainedSplitRefreshFacts, error) {
	numbers := make([]string, 0, len(dependencies))
	for number := range dependencies {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	ids := make([]string, len(numbers))
	for i, number := range numbers {
		ids[i] = dependencies[number]
	}
	rows, err := q.Query(ctx, retainedAdmissionRefreshSQL, append(ordersScopeArgs(scope), numbers, ids)...)
	if err != nil {
		return nil, ordersError("read refreshed split admission", err)
	}
	defer rows.Close()
	result := make([]orders.RetainedSplitRefreshFacts, 0, len(numbers))
	for rows.Next() {
		f, e := decodeRetainedAdmissionRefresh(rows)
		if e != nil {
			if historicalSplitUnavailable(e) {
				continue
			}
			return nil, e
		}
		result = append(result, f)
	}
	return result, ordersRowsError("finish refreshed split admissions", rows.Err())
}

func decodeRetainedAdmissionRefresh(row pgx.Row) (orders.RetainedSplitRefreshFacts, error) {
	var f orders.RetainedSplitRefreshFacts
	var id string
	var split, before, correlation, parent, children, admission, source, target, refresh, refreshSource, refreshTarget, history []byte
	if err := row.Scan(&id, &split, &before, &correlation, &parent, &children, &admission, &source, &target, &f.Root.SplitReconciledAt, &f.Root.NativeCreatedAt, &f.Root.NativeDispatchedAt, &f.Root.NativeReconciledAt, &refresh, &refreshSource, &refreshTarget, &history); err != nil {
		return f, ordersError("decode refreshed split admission", err)
	}
	for _, pair := range []struct {
		raw []byte
		out any
	}{
		{split, &f.Root.Split}, {before, &f.Root.Before}, {correlation, &f.Root.OriginalCorrelation}, {parent, &f.Root.Parent}, {children, &f.Root.Children}, {admission, &f.Root.Admission}, {source, &f.Root.AdmissionSource}, {target, &f.Root.Target}, {refresh, &f.Refresh}, {refreshSource, &f.RefreshSource}, {refreshTarget, &f.Target}, {history, &f.History},
	} {
		if ordersDecode(pair.raw, pair.out) != nil {
			return f, orders.ErrStale
		}
	}
	if id != f.Root.Split.ID {
		return f, orders.ErrStale
	}
	return f, nil
}
