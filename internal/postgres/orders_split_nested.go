package postgres

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

const nestedSplitWorkSQL = `SELECT w.id,w.work,
 (decision->'posting')-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],
 w.created_at,w.dispatched_at,(SELECT max(r.created_at) FROM orders_reconciliations r
 WHERE r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account)
 FROM orders_work seed JOIN orders_work w ON w.tenant_id=seed.tenant_id
 AND w.moysklad_account=seed.moysklad_account AND w.ozon_account=seed.ozon_account
 AND w.work#>>'{command,commercial_order_id}'=seed.work#>>'{command,commercial_order_id}'
 JOIN orders_previews preview ON preview.id=w.work->>'preview_id' AND preview.tenant_id=w.tenant_id
 AND preview.moysklad_account=w.moysklad_account AND preview.ozon_account=w.ozon_account
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(preview.preview->'decisions')='array' THEN preview.preview->'decisions' ELSE '[]'::jsonb END) decision
 WHERE seed.tenant_id=$1 AND seed.moysklad_account=$2 AND seed.ozon_account=$3 AND seed.id=$4
 AND seed.action IN ('posting_split','posting_split_unassembled') AND seed.dispatch_count=1
 AND w.action IN ('posting_split','posting_split_unassembled') AND w.dispatch_count=1
 AND w.state NOT IN ('REJECTED','BLOCKED')
 AND decision#>>'{posting,posting_number}'=w.posting_number
 AND decision#>>'{posting,evidence,revision}'=w.work#>>'{command,expected_source_revision}'
 ORDER BY w.created_at,w.id LIMIT 65`

type nestedWorkCandidate struct {
	step    orders.SplitLineageStep
	attempt orders.NestedSplitAttempt
}

func enrichNestedSplitSettlements(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, dependencies map[string]string) error {
	cache := make(map[string]orders.NestedSplitSettlementFacts)
	for number, id := range dependencies {
		facts, found := cache[id]
		if !found {
			var err error
			facts, err = readNestedSplitSettlement(ctx, q, result.Config.Scope, id)
			if err != nil {
				if historicalSplitUnavailable(err) {
					continue
				}
				return err
			}
			for _, step := range facts.Lineage.Splits {
				cache[step.Work.ID] = facts
			}
		}
		facts.Lineage.SelectedPosting, facts.SelectedSplitWorkID = number, id
		applyNestedSplitSettlement(result, dependencies, facts, number)
	}
	return nil
}

func applyNestedSplitSettlement(result *orders.ReviewContext, dependencies map[string]string, facts orders.NestedSplitSettlementFacts, number string) {
	for _, posting := range result.Postings {
		if posting.PostingNumber != number {
			continue
		}
		permission, err := orders.NewNestedSplitSettlement(facts, posting)
		if err != nil {
			return
		}
		result.SplitReservePermissions[number] = permission
		delete(result.SplitReserveBlocked, number)
		delete(dependencies, number)
		return
	}
}

func readNestedSplitSettlement(ctx context.Context, q ordersQuerier, scope orders.Scope, seed string) (orders.NestedSplitSettlementFacts, error) {
	var result orders.NestedSplitSettlementFacts
	candidates, err := readNestedSplitCandidates(ctx, q, scope, seed)
	if err != nil {
		return result, err
	}
	candidates = connectedNestedCandidates(candidates, seed)
	if len(candidates) < 2 || len(candidates) > orders.MaxSplitLineageSteps {
		return result, orders.ErrBlocked
	}
	result.Lineage.Scope, result.Lineage.RootPosting = scope, candidates[0].step.Before.PostingNumber
	for _, candidate := range candidates {
		work := candidate.step.Work
		if err = hydrateHistoricalSplitWork(ctx, q, scope, &work); err != nil {
			return result, err
		}
		candidate.step.Work = work
		result.Lineage.Splits = append(result.Lineage.Splits, candidate.step)
		result.Attempts = append(result.Attempts, candidate.attempt)
	}
	numbers, parents := nestedSplitNumbers(result.Lineage.Splits)
	result.Lineage.CurrentHeads, err = readNestedSplitHeads(ctx, q, scope, numbers)
	if err != nil {
		return result, err
	}
	result.NativeHistory, err = readNestedSplitNativeHistory(ctx, q, scope, parents)
	if err != nil {
		return result, err
	}
	err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&result.CheckedAt)
	return result, ordersRowsError("read nested settlement clock", err)
}

func readNestedSplitCandidates(ctx context.Context, q ordersQuerier, scope orders.Scope, seed string) ([]nestedWorkCandidate, error) {
	rows, err := q.Query(ctx, nestedSplitWorkSQL, append(ordersScopeArgs(scope), seed)...)
	if err != nil {
		return nil, ordersError("read nested split works", err)
	}
	defer rows.Close()
	result := make([]nestedWorkCandidate, 0)
	for rows.Next() {
		candidate, e := decodeNestedSplitCandidate(rows)
		if e != nil {
			return nil, e
		}
		if len(result) >= orders.MaxSplitLineageSteps {
			return nil, orders.ErrBlocked
		}
		result = append(result, candidate)
	}
	return result, ordersRowsError("finish nested split works", rows.Err())
}

func decodeNestedSplitCandidate(row pgx.Row) (nestedWorkCandidate, error) {
	var result nestedWorkCandidate
	var raw, before []byte
	var dispatched, reconciled *time.Time
	if err := row.Scan(&result.attempt.WorkID, &raw, &before, &result.attempt.CreatedAt, &dispatched, &reconciled); err != nil {
		return result, ordersError("decode nested split work", err)
	}
	if ordersDecode(raw, &result.step.Work) != nil || ordersDecode(before, &result.step.Before) != nil || result.step.Work.ID != result.attempt.WorkID {
		return result, orders.ErrStale
	}
	if dispatched != nil {
		result.attempt.DispatchedAt = *dispatched
	}
	if reconciled != nil {
		result.attempt.ReconciledAt = *reconciled
	}
	return result, nil
}

func connectedNestedCandidates(all []nestedWorkCandidate, seed string) []nestedWorkCandidate {
	numbers := make(map[string]bool)
	selected := make(map[string]bool)
	for _, candidate := range all {
		if candidate.step.Work.ID == seed {
			addNestedCandidate(numbers, selected, candidate)
		}
	}
	for pass := 0; pass < len(all); pass++ {
		changed := false
		for _, candidate := range all {
			if selected[candidate.step.Work.ID] || !nestedCandidateConnected(numbers, candidate) {
				continue
			}
			addNestedCandidate(numbers, selected, candidate)
			changed = true
		}
		if !changed {
			break
		}
	}
	return slices.DeleteFunc(slices.Clone(all), func(candidate nestedWorkCandidate) bool { return !selected[candidate.step.Work.ID] })
}

func nestedCandidateConnected(numbers map[string]bool, candidate nestedWorkCandidate) bool {
	if numbers[candidate.step.Before.PostingNumber] {
		return true
	}
	return slices.ContainsFunc(candidate.step.Work.Receipt.PostingNumbers, func(number string) bool { return numbers[number] })
}

func addNestedCandidate(numbers, selected map[string]bool, candidate nestedWorkCandidate) {
	selected[candidate.step.Work.ID] = true
	numbers[candidate.step.Before.PostingNumber] = true
	for _, number := range candidate.step.Work.Receipt.PostingNumbers {
		numbers[number] = true
	}
}

func nestedSplitNumbers(steps []orders.SplitLineageStep) ([]string, []string) {
	numbers, parents := make([]string, 0), make([]string, 0)
	for _, step := range steps {
		parents = append(parents, step.Before.PostingNumber)
		numbers = append(numbers, step.Before.PostingNumber)
		numbers = append(numbers, step.Work.Receipt.PostingNumbers...)
	}
	slices.Sort(numbers)
	slices.Sort(parents)
	return slices.Compact(numbers), slices.Compact(parents)
}

func readNestedSplitHeads(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string) ([]orders.Posting, error) {
	if len(numbers) > orders.MaxSplitLineageHeads {
		return nil, orders.ErrBlocked
	}
	rows, err := q.Query(ctx, `SELECT h.projection-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],h.checked_at
 FROM orders_posting_heads h WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3
 AND h.posting_number=ANY($4::text[]) AND NOT h.quarantined ORDER BY h.posting_number`, append(ordersScopeArgs(scope), numbers)...)
	if err != nil {
		return nil, ordersError("read nested split heads", err)
	}
	defer rows.Close()
	result := make([]orders.Posting, 0, len(numbers))
	for rows.Next() {
		var posting orders.Posting
		var raw []byte
		var checked time.Time
		if err = rows.Scan(&raw, &checked); err != nil {
			return nil, ordersError("decode nested split head", err)
		}
		if err = ordersDecode(raw, &posting); err != nil {
			return nil, err
		}
		posting.Evidence.CheckedAt = checked
		result = append(result, posting)
	}
	return result, ordersRowsError("finish nested split heads", rows.Err())
}

func readNestedSplitNativeHistory(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string) ([]orders.NestedSplitNativeHistory, error) {
	rows, err := q.Query(ctx, `SELECT parent.number,
 ARRAY(SELECT binding.customer_order_id FROM orders_projection_bindings binding WHERE binding.tenant_id=$1 AND binding.moysklad_account=$2 AND binding.ozon_account=$3 AND binding.posting_number=parent.number AND binding.customer_order_id IS NOT NULL),
 ARRAY(SELECT w.id FROM orders_work w WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=parent.number AND w.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update') ORDER BY w.id)
 FROM unnest($4::text[]) parent(number) ORDER BY parent.number`, append(ordersScopeArgs(scope), numbers)...)
	if err != nil {
		return nil, ordersError("read nested split native history", err)
	}
	defer rows.Close()
	result := make([]orders.NestedSplitNativeHistory, 0, len(numbers))
	for rows.Next() {
		history := orders.NestedSplitNativeHistory{Scope: scope}
		if err = rows.Scan(&history.PostingNumber, &history.BindingIDs, &history.MirrorWorkIDs); err != nil {
			return nil, ordersError("decode nested split native history", err)
		}
		result = append(result, history)
	}
	return result, ordersRowsError("finish nested split native history", rows.Err())
}
