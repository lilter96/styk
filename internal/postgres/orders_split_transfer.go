package postgres

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// The selected split is the dependency discovered by the ordinary lineage
// guard. Parent release and binding stay scoped to the same exact account pair.
// No network request or mutable 'transferred' flag establishes this proof.
const splitTransferCandidatesSQL = `SELECT selected.number,split.work,
 parent.projection,parent.checked_at,parent.quarantined,release.work,release.target
FROM unnest($4::text[],$5::text[]) selected(number,split_id)
JOIN orders_work split ON split.id=selected.split_id AND split.tenant_id=$1
 AND split.moysklad_account=$2 AND split.ozon_account=$3 AND split.state='RECONCILED'
 AND split.action IN ('posting_split','posting_split_unassembled') AND split.dispatch_count=1 AND split.posting_number<>selected.number
JOIN orders_posting_heads parent ON parent.tenant_id=split.tenant_id
 AND parent.moysklad_account=split.moysklad_account AND parent.ozon_account=split.ozon_account
 AND parent.posting_number=split.posting_number
JOIN LATERAL (SELECT w.work,r.observed_target AS target
 FROM orders_work w JOIN orders_reconciliations r ON r.work_id=w.id
  AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
 WHERE w.tenant_id=split.tenant_id AND w.moysklad_account=split.moysklad_account
  AND w.ozon_account=split.ozon_account AND w.posting_number=split.posting_number
  AND w.action='mirror_position_update' AND w.state='RECONCILED' AND w.dispatch_count=1
  AND (w.work#>>'{command,retained_split_work_id}'=split.id
   OR w.work#>>'{command,superseded_split_work_id}'=split.id) AND r.observed_target IS NOT NULL
 ORDER BY r.created_at DESC,w.id DESC LIMIT 1) release ON true
JOIN orders_projection_bindings binding ON binding.tenant_id=split.tenant_id
 AND binding.moysklad_account=split.moysklad_account AND binding.ozon_account=split.ozon_account
 AND binding.posting_number=split.posting_number
 AND binding.customer_order_id=release.work#>>'{command,correlation,customer_order_id}'
WHERE NOT EXISTS (SELECT 1 FROM orders_target_fences f WHERE f.tenant_id=split.tenant_id
 AND f.moysklad_account=split.moysklad_account AND f.ozon_account=split.ozon_account
 AND f.posting_number=split.posting_number)
ORDER BY selected.number LIMIT 10001`

func enrichOrderSplitTransfers(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string) error {
	if err := enrichReducedSplitTransfers(ctx, q, result, dependencies); err != nil {
		return err
	}
	if err := enrichRetainedSplitAdmissions(ctx, q, result, dependencies); err != nil {
		return err
	}
	return enrichRetainedAdmissionRefreshes(ctx, q, result, dependencies)
}

func enrichReducedSplitTransfers(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string) error {
	if len(dependencies) == 0 {
		return nil
	}
	candidates, err := readOrderSplitTransferCandidates(ctx, q, result.Config.Scope, dependencies)
	if err != nil || len(candidates) == 0 {
		return err
	}
	parents := orders.ReviewContext{Config: result.Config}
	numbers := make([]string, 0, len(candidates))
	for _, facts := range candidates {
		numbers = append(numbers, facts.Parent.PostingNumber)
	}
	slices.Sort(numbers)
	numbers = slices.Compact(numbers)
	if err = enrichOrderRetainedSplitReserves(ctx, q, &parents, numbers); err != nil {
		return err
	}
	for _, facts := range candidates {
		parents.Postings = append(parents.Postings, facts.Parent)
	}
	if err = enrichOrderSupersededSplitReserves(ctx, q, &parents, numbers); err != nil {
		return err
	}
	applyOrderSplitTransferProofs(result, parents, candidates)
	return nil
}

func applyOrderSplitTransferProofs(result *orders.ReviewContext, parents orders.ReviewContext, candidates []orders.SplitReserveTransferFacts) {
	children := make(map[string]orders.Posting, len(result.Postings))
	for _, posting := range result.Postings {
		children[posting.PostingNumber] = posting
	}
	result.SplitReserveTransfers = make(map[string]orders.SplitReserveTransferPermission, len(candidates))
	for _, facts := range candidates {
		facts.Child = children[facts.Child.PostingNumber]
		if facts.ParentRelease.Command.SupersededSplitWorkID != "" {
			applySupersededSplitTransfer(result, parents, facts)
			continue
		}
		permit := parents.SplitRetainedReserves[facts.Parent.PostingNumber]
		facts.Before, facts.OriginalCorrelation = permit.Before, permit.Correlation
		if permit.SplitWorkID != facts.Split.ID || facts.ParentTarget.Fields.StoreID != result.Config.StoreID || facts.ParentTarget.Fields.OrganizationID != result.Config.OrganizationID {
			continue
		}
		if proof, e := orders.NewSplitReserveTransfer(facts); e == nil {
			result.SplitReserveTransfers[facts.Child.PostingNumber] = proof
			delete(result.SplitReserveBlocked, facts.Child.PostingNumber)
		}
	}
}

func applySupersededSplitTransfer(result *orders.ReviewContext, parents orders.ReviewContext, facts orders.SplitReserveTransferFacts) {
	retirement, found := parents.SplitSupersededReserves[facts.Parent.PostingNumber]
	if !found || retirement.Split.ID != facts.Split.ID || facts.ParentTarget.Fields.StoreID != result.Config.StoreID || facts.ParentTarget.Fields.OrganizationID != result.Config.OrganizationID {
		return
	}
	facts.Before, facts.OriginalCorrelation = retirement.Before, retirement.OriginalCorrelation
	baseline, err := orders.ProjectionPositionBaseline(facts.ParentRelease, facts.ParentTarget)
	if err != nil {
		return
	}
	retirement.Baseline, retirement.Target = baseline, facts.ParentTarget
	if proof, e := orders.NewSupersededSplitTransfer(facts, retirement); e == nil {
		result.SplitReserveTransfers[facts.Child.PostingNumber] = proof
		delete(result.SplitReserveBlocked, facts.Child.PostingNumber)
	}
}

func readOrderSplitTransferCandidates(ctx context.Context, q ordersQuerier, scope orders.Scope, dependencies map[string]string) ([]orders.SplitReserveTransferFacts, error) {
	numbers := make([]string, 0, len(dependencies))
	for number := range dependencies {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	ids := make([]string, len(numbers))
	for i, number := range numbers {
		ids[i] = dependencies[number]
	}
	rows, err := q.Query(ctx, splitTransferCandidatesSQL, append(ordersScopeArgs(scope), numbers, ids)...)
	if err != nil {
		return nil, ordersError("read split reserve transfer candidates", err)
	}
	defer rows.Close()
	result := make([]orders.SplitReserveTransferFacts, 0, len(numbers))
	for rows.Next() {
		facts, quarantined, e := decodeOrderSplitTransferCandidate(rows)
		if e != nil {
			return nil, e
		}
		if !quarantined {
			result = append(result, facts)
		}
	}
	if err = ordersRowsError("read split reserve transfer candidates", rows.Err()); err != nil {
		return nil, err
	}
	rows.Close()
	return hydrateSplitTransferCandidates(ctx, q, scope, result)
}

func decodeOrderSplitTransferCandidate(row pgx.Row) (orders.SplitReserveTransferFacts, bool, error) {
	var result orders.SplitReserveTransferFacts
	var number string
	var rawSplit, rawParent, rawRelease, rawTarget []byte
	var checked time.Time
	var quarantined bool
	if err := row.Scan(&number, &rawSplit, &rawParent, &checked, &quarantined, &rawRelease, &rawTarget); err != nil {
		return result, false, ordersError("decode split reserve transfer candidate", err)
	}
	for _, value := range []struct {
		raw []byte
		out any
	}{{rawSplit, &result.Split}, {rawParent, &result.Parent}, {rawRelease, &result.ParentRelease}, {rawTarget, &result.ParentTarget}} {
		if err := ordersDecode(value.raw, value.out); err != nil {
			return result, false, err
		}
	}
	result.Parent.Evidence.CheckedAt = checked
	result.Child.PostingNumber = number
	return result, quarantined, nil
}

func hydrateSplitTransferCandidates(ctx context.Context, q ordersQuerier, scope orders.Scope, candidates []orders.SplitReserveTransferFacts) ([]orders.SplitReserveTransferFacts, error) {
	result := make([]orders.SplitReserveTransferFacts, 0, len(candidates))
	cache := make(map[string]orders.Work)
	blocked := make(map[string]bool)
	for _, facts := range candidates {
		if blocked[facts.Split.ID] {
			continue
		}
		split, found := cache[facts.Split.ID]
		if !found {
			split = facts.Split
			if err := hydrateHistoricalSplitWork(ctx, q, scope, &split); err != nil {
				if historicalSplitUnavailable(err) {
					blocked[split.ID] = true
					continue
				}
				return nil, err
			}
			cache[split.ID] = split
		}
		facts.Split = split
		result = append(result, facts)
	}
	return result, nil
}
