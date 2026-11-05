package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// A first retained-root create has no native baseline. Its exact source split
// marker must be reconstructed both before approval and before dispatch.
func enrichRetainedRootAdmissions(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	bound := make(map[string]bool, len(result.Correlations))
	for _, correlation := range result.Correlations {
		if correlation.Scope == result.Config.Scope && correlation.CustomerOrderID != "" {
			bound[correlation.PostingNumber] = true
		}
	}
	for _, number := range numbers {
		if bound[number] {
			continue
		}
		facts, err := readRetainedRootAdmission(ctx, q, result, number)
		if historicalSplitUnavailable(err) {
			continue
		}
		if err != nil {
			return err
		}
		applyRetainedRootAdmission(result, facts, number)
	}
	return nil
}

func applyRetainedRootAdmission(result *orders.ReviewContext, facts orders.NestedSplitSettlementFacts, number string) {
	for _, posting := range result.Postings {
		if posting.PostingNumber != number {
			continue
		}
		permission, err := orders.NewRetainedRootSplitReserve(facts, posting)
		if err != nil {
			return
		}
		result.SplitReservePermissions[number] = permission
		delete(result.SplitReserveBlocked, number)
		return
	}
}

func readRetainedRootAdmission(ctx context.Context, q ordersQuerier, current *orders.ReviewContext, number string) (orders.NestedSplitSettlementFacts, error) {
	scope := current.Config.Scope
	var seed string
	err := q.QueryRow(ctx, `SELECT id FROM orders_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4 AND action='posting_split_unassembled' AND dispatch_count=1 AND state NOT IN('BLOCKED','REJECTED') ORDER BY created_at DESC,id DESC LIMIT 1`, append(ordersScopeArgs(scope), number)...).Scan(&seed)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.NestedSplitSettlementFacts{}, orders.ErrBlocked
	}
	if err != nil {
		return orders.NestedSplitSettlementFacts{}, ordersError("read retained root admission seed", err)
	}
	facts, err := readNestedSplitLineage(ctx, q, scope, seed)
	if err != nil || facts.Lineage.RootPosting != number {
		if err == nil {
			err = orders.ErrBlocked
		}
		return facts, err
	}
	facts.Lineage.SelectedPosting, facts.SelectedSplitWorkID = number, seed
	facts.NativeHistory, err = readRetainedRootNativeHistory(ctx, q, scope, number, seed, current.RevalidationWorkID, current.RevalidationCommandFingerprint)
	if err != nil {
		return facts, err
	}
	err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&facts.CheckedAt)
	return facts, ordersRowsError("read retained root admission clock", err)
}

// Every other state is an existence witness. Only this exact fenced, unsent
// create can be omitted; neither a missing marker nor a lost response is absence.
const retainedRootNativeHistorySQL = `SELECT
 ARRAY(SELECT b.customer_order_id FROM orders_projection_bindings b
 WHERE b.tenant_id=$1 AND b.moysklad_account=$2 AND b.ozon_account=$3
 AND b.posting_number=$4 AND b.customer_order_id IS NOT NULL),
 ARRAY(SELECT w.id FROM orders_work w
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=$4
 AND w.action IN('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
 AND NOT COALESCE((w.id=$6 AND w.command_fingerprint=$7 AND w.dispatch_count=0
 AND w.state IN('QUEUED','CLAIMED') AND w.action='mirror_create'
 AND w.work#>>'{command,split_source_work_id}'=$5
 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id
 AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account
 AND f.posting_number=w.posting_number)),false) LIMIT 1)`

func readRetainedRootNativeHistory(ctx context.Context, q ordersQuerier, scope orders.Scope, number, split, selfID, fingerprint string) ([]orders.NestedSplitNativeHistory, error) {
	history := orders.NestedSplitNativeHistory{Scope: scope, PostingNumber: number}
	err := q.QueryRow(ctx, retainedRootNativeHistorySQL, append(ordersScopeArgs(scope), number, split, selfID, fingerprint)...).Scan(&history.BindingIDs, &history.MirrorWorkIDs)
	if err != nil {
		return nil, ordersRowsError("read retained root native absence", err)
	}
	return []orders.NestedSplitNativeHistory{history}, nil
}
