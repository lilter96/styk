package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type ownedAncestorNativeRow struct {
	work   orders.Work
	target orders.TargetObservation
}

func enrichOwnedAncestorSettlements(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	if result.SplitAncestorSettlements == nil {
		result.SplitAncestorSettlements = make(map[string]orders.SplitAncestorSettlementPermission)
	}
	for _, number := range numbers {
		facts, err := readOwnedAncestorSettlement(ctx, q, result.Config.Scope, number, result.RevalidationWorkID, result.RevalidationCommandFingerprint)
		if err != nil {
			if historicalSplitUnavailable(err) {
				continue
			}
			return err
		}
		if !ownedAncestorMappingsCurrent(facts, result) {
			continue
		}
		proof, err := orders.NewSplitAncestorSettlement(facts)
		if err != nil {
			continue
		}
		result.SplitAncestorSettlements[number] = proof
		delete(result.SplitReserveBlocked, number)
	}
	return nil
}

func readOwnedAncestorSettlement(ctx context.Context, q ordersQuerier, scope orders.Scope, number, selfID, selfFingerprint string) (orders.SplitAncestorSettlementFacts, error) {
	var result orders.SplitAncestorSettlementFacts
	var seed string
	err := q.QueryRow(ctx, `SELECT id FROM orders_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND action='posting_split_unassembled' AND dispatch_count=1 AND state NOT IN('BLOCKED','REJECTED') AND (posting_number=$4 OR COALESCE(work#>'{receipt,posting_numbers}','[]'::jsonb) ? $4) ORDER BY created_at DESC,id DESC LIMIT 1`, append(ordersScopeArgs(scope), number)...).Scan(&seed)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, orders.ErrBlocked
	}
	if err != nil {
		return result, ordersError("read owned split seed", err)
	}
	nested, err := readNestedSplitSettlement(ctx, q, scope, seed)
	if err != nil {
		return result, err
	}
	if len(nested.Lineage.Splits) != 2 || len(nested.Lineage.CurrentHeads) != 3 {
		return result, orders.ErrBlocked
	}
	result.CheckedAt = nested.CheckedAt
	result.Lineage = nested.Lineage
	result.Lineage.SelectedPosting = number
	root, node := result.Lineage.Splits[0], result.Lineage.Splits[1]
	result.NodeSplit = node
	for _, head := range result.Lineage.CurrentHeads {
		if head.PostingNumber == number {
			result.Selected = head
		}
		if head.PostingNumber == node.Before.PostingNumber {
			result.CurrentNode = head
		}
	}
	rows, err := readOwnedAncestorNativeRows(ctx, q, scope, []string{root.Before.PostingNumber, node.Before.PostingNumber}, selfID, selfFingerprint, node.Before.PostingNumber, node.Work.ID)
	if err != nil {
		return result, err
	}
	if !assignOwnedAncestorRows(&result, rows, root) {
		return result, orders.ErrBlocked
	}
	return result, nil
}

func readOwnedAncestorNativeRows(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string, selfID, selfFingerprint, node, splitID string) ([]ownedAncestorNativeRow, error) {
	rows, err := q.Query(ctx, `SELECT w.work,r.observed_target,EXISTS(SELECT 1 FROM orders_projection_bindings b WHERE b.tenant_id=w.tenant_id AND b.moysklad_account=w.moysklad_account AND b.ozon_account=w.ozon_account AND b.posting_number=w.posting_number AND b.customer_order_id=r.observed_target#>>'{correlation,customer_order_id}') FROM orders_work w LEFT JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=ANY($4::text[]) AND w.action IN('mirror_create','mirror_update','mirror_position_update','mirror_composition_update') AND NOT(w.id=$5 AND w.command_fingerprint=$6 AND w.dispatch_count=0 AND w.state IN('QUEUED','CLAIMED') AND w.action='mirror_position_update' AND w.posting_number=$7 AND w.work#>>'{command,split_ancestor_settlement,mode}'='retained_reduction' AND w.work#>>'{command,split_ancestor_settlement,split_work_id}'=$8 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account AND f.posting_number=w.posting_number)) ORDER BY w.created_at,w.id LIMIT 5`, append(ordersScopeArgs(scope), numbers, selfID, selfFingerprint, node, splitID)...)
	if err != nil {
		return nil, ordersError("read owned ancestor native history", err)
	}
	defer rows.Close()
	result := make([]ownedAncestorNativeRow, 0, 4)
	for rows.Next() {
		var row ownedAncestorNativeRow
		var work, target []byte
		var bindingCurrent bool
		if err = rows.Scan(&work, &target, &bindingCurrent); err != nil {
			return nil, ordersError("decode owned native history", err)
		}
		if !bindingCurrent || ordersDecode(work, &row.work) != nil || len(target) == 0 || ordersDecode(target, &row.target) != nil {
			return nil, orders.ErrBlocked
		}
		result = append(result, row)
	}
	return result, ordersRowsError("finish owned ancestor native history", rows.Err())
}

func assignOwnedAncestorRows(f *orders.SplitAncestorSettlementFacts, rows []ownedAncestorNativeRow, root orders.SplitLineageStep) bool {
	if len(rows) < 3 || len(rows) > 4 {
		return false
	}
	f.RootTransfer.Split, f.RootTransfer.Before, f.RootTransfer.OriginalCorrelation = root.Work, root.Before, root.Work.Command.Correlation
	f.RootTransfer.Child = f.NodeSplit.Before
	for _, head := range f.Lineage.CurrentHeads {
		if head.PostingNumber == root.Before.PostingNumber {
			f.RootTransfer.Parent = head
		}
	}
	for _, row := range rows {
		if !assignOwnedAncestorRow(f, row, root) {
			return false
		}
	}
	f.HistoryUnambiguous = f.RootOrigin.ID != "" && f.RootTransfer.ParentRelease.ID != "" && f.NodeOrigin.ID != ""
	return f.HistoryUnambiguous
}

func ownedAncestorMappingsCurrent(f orders.SplitAncestorSettlementFacts, current *orders.ReviewContext) bool {
	for _, w := range []orders.Work{f.RootTransfer.ParentRelease, f.NodeOrigin} {
		if w.Command.CustomerOrder == nil || !retainedAdmissionMappingsCurrent(orders.RetainedSplitAdmissionFacts{Admission: w}, current) {
			return false
		}
	}
	return true
}

func assignOwnedAncestorRow(f *orders.SplitAncestorSettlementFacts, row ownedAncestorNativeRow, root orders.SplitLineageStep) bool {
	w := row.work
	if w.State != orders.StateReconciled || w.DispatchCount != 1 {
		return false
	}
	switch {
	case w.Command.PostingNumber == root.Before.PostingNumber && w.Command.Action == orders.ActionMirrorCreate:
		if f.RootOrigin.ID != "" {
			return false
		}
		f.RootOrigin, f.RootOriginTarget = w, row.target
	case w.Command.PostingNumber == root.Before.PostingNumber && w.Command.Action == orders.ActionMirrorPositionUpdate && w.Command.RetainedSplitWorkID == root.Work.ID:
		if f.RootTransfer.ParentRelease.ID != "" {
			return false
		}
		f.RootTransfer.ParentRelease, f.RootTransfer.ParentTarget = w, row.target
	case w.Command.PostingNumber == f.NodeSplit.Before.PostingNumber && w.Command.Action == orders.ActionMirrorCreate:
		if f.NodeOrigin.ID != "" {
			return false
		}
		f.NodeOrigin, f.NodeOriginTarget = w, row.target
	case w.Command.PostingNumber == f.NodeSplit.Before.PostingNumber && w.Command.Action == orders.ActionMirrorPositionUpdate && w.Command.SplitAncestorSettlement != nil:
		if f.NodeReduction != nil {
			return false
		}
		copyWork, copyTarget := w, row.target
		f.NodeReduction, f.NodeReductionTarget = &copyWork, &copyTarget
	default:
		return false
	}
	return true
}
