package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lilter96/styk/internal/orders"
)

// Structural ancestry is reconstructed from immutable acknowledgements. Provider
// parent fields are optional and are never manufactured to select a path.
func ownedAncestorPath(lineage orders.SplitLineageFacts, number string) ([]orders.SplitLineageStep, error) {
	lineage.SelectedPosting = number
	proof, err := orders.NewSplitLineage(lineage)
	if err != nil {
		return nil, err
	}
	ids := proof.PathWorkIDs()
	if len(ids) < 2 || len(ids) > orders.MaxSplitLineageDepth {
		return nil, orders.ErrBlocked
	}
	byID := make(map[string]orders.SplitLineageStep, len(lineage.Splits))
	for _, step := range lineage.Splits {
		byID[step.Work.ID] = step
	}
	result := make([]orders.SplitLineageStep, 0, len(ids))
	for _, id := range ids {
		step, exists := byID[id]
		if !exists {
			return nil, orders.ErrBlocked
		}
		result = append(result, step)
	}
	return result, nil
}

func loadOwnedAncestorPath(ctx context.Context, q ordersQuerier, nested orders.NestedSplitSettlementFacts, number, selfID, selfFingerprint string) (orders.SplitAncestorSettlementFacts, error) {
	f := orders.SplitAncestorSettlementFacts{Lineage: nested.Lineage, CheckedAt: nested.CheckedAt, Attempts: nested.Attempts}
	f.Lineage.SelectedPosting = number
	path, err := ownedAncestorPath(f.Lineage, number)
	if err != nil {
		return f, err
	}
	f.NodeSplit = path[len(path)-1]
	numbers := make([]string, len(path))
	for i, step := range path {
		numbers[i] = step.Before.PostingNumber
	}
	rows, err := readOwnedAncestorNativeRows(ctx, q, f.Lineage.Scope, numbers, selfID, selfFingerprint, f.NodeSplit.Before.PostingNumber, f.NodeSplit.Work.ID)
	if err != nil {
		return f, err
	}
	for _, row := range rows {
		f.NativeAttempts = append(f.NativeAttempts, row.attempt)
	}
	if err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&f.CheckedAt); err != nil {
		return f, ordersRowsError("read owned ancestor settlement clock", err)
	}
	assignOwnedAncestorHeads(&f, number)
	if ownedPathRepeats(path) {
		if !assignRetainedOwnedHistory(&f, rows, path) {
			return f, orders.ErrBlocked
		}
		return f, nil
	}
	if len(path) == 2 {
		if !assignOwnedAncestorRows(&f, rows, path[0]) {
			return f, orders.ErrBlocked
		}
		return f, nil
	}
	if !assignDeepOwnedAncestorRows(&f, rows, path) {
		return f, orders.ErrBlocked
	}
	return f, nil
}

func assignDeepOwnedAncestorRows(f *orders.SplitAncestorSettlementFacts, rows []ownedAncestorNativeRow, path []orders.SplitLineageStep) bool {
	grouped := make(map[string][]ownedAncestorNativeRow, len(path))
	for _, row := range rows {
		if row.work.State != orders.StateReconciled || row.work.DispatchCount != 1 {
			return false
		}
		number := row.work.Command.PostingNumber
		grouped[number] = append(grouped[number], row)
	}
	root := path[0]
	f.RootTransfer.Split, f.RootTransfer.Before, f.RootTransfer.OriginalCorrelation = root.Work, root.Before, root.Work.Command.Correlation
	f.RootTransfer.Child = path[1].Before
	for _, head := range f.Lineage.CurrentHeads {
		if head.PostingNumber == root.Before.PostingNumber {
			f.RootTransfer.Parent = head
		}
	}
	if !assignDeepOwnedRoot(f, grouped[root.Before.PostingNumber], root) {
		return false
	}
	f.Path = make([]orders.SplitAncestorGeneration, 0, len(path)-1)
	for _, split := range path[1:] {
		generation, ok := ownedAncestorGeneration(split, grouped[split.Before.PostingNumber], f.Lineage.CurrentHeads)
		if !ok {
			return false
		}
		f.Path = append(f.Path, generation)
	}
	last := f.Path[len(f.Path)-1]
	f.NodeOrigin, f.NodeOriginTarget = last.Origin, last.OriginTarget
	f.NodeReduction, f.NodeReductionTarget = last.Reduction, last.ReductionTarget
	f.HistoryUnambiguous = true
	return true
}

func assignDeepOwnedRoot(f *orders.SplitAncestorSettlementFacts, rows []ownedAncestorNativeRow, root orders.SplitLineageStep) bool {
	if len(rows) != 2 {
		return false
	}
	for _, row := range rows {
		w := row.work
		switch {
		case w.Command.Action == orders.ActionMirrorCreate && f.RootOrigin.ID == "":
			f.RootOrigin, f.RootOriginTarget = w, row.target
		case w.Command.Action == orders.ActionMirrorPositionUpdate && w.Command.RetainedSplitWorkID == root.Work.ID && f.RootTransfer.ParentRelease.ID == "":
			f.RootTransfer.ParentRelease, f.RootTransfer.ParentTarget = w, row.target
		default:
			return false
		}
	}
	return f.RootOrigin.ID != "" && f.RootTransfer.ParentRelease.ID != ""
}

func ownedAncestorGeneration(split orders.SplitLineageStep, rows []ownedAncestorNativeRow, heads []orders.Posting) (orders.SplitAncestorGeneration, bool) {
	result := orders.SplitAncestorGeneration{Split: split}
	if len(rows) < 1 || len(rows) > 2 {
		return result, false
	}
	for _, row := range rows {
		w := row.work
		switch {
		case w.Command.Action == orders.ActionMirrorCreate && result.Origin.ID == "":
			result.Origin, result.OriginTarget = w, row.target
		case w.Command.Action == orders.ActionMirrorPositionUpdate && w.Command.SplitAncestorSettlement != nil && w.Command.SplitAncestorSettlement.SplitWorkID == split.Work.ID && w.Command.SplitAncestorSettlement.Mode == orders.SplitAncestorReduce && result.Reduction == nil:
			work, target := w, row.target
			result.Reduction, result.ReductionTarget = &work, &target
		default:
			return result, false
		}
	}
	for _, head := range heads {
		if head.PostingNumber == split.Before.PostingNumber {
			result.Current = head
		}
	}
	return result, result.Origin.ID != "" && result.Current.PostingNumber == split.Before.PostingNumber
}

func decodeOwnedAncestorNativeRow(raw pgx.Row, scope orders.Scope) (ownedAncestorNativeRow, error) {
	var row ownedAncestorNativeRow
	var work, target []byte
	var bindingCurrent bool
	var dispatched, reconciled *time.Time
	if err := raw.Scan(&work, &target, &row.attempt.CreatedAt, &dispatched, &reconciled, &bindingCurrent); err != nil {
		return row, ordersError("decode owned native history", err)
	}
	if !bindingCurrent || ordersDecode(work, &row.work) != nil || len(target) == 0 || ordersDecode(target, &row.target) != nil || row.work.Scope != scope || row.work.Command.Scope != scope {
		return row, orders.ErrBlocked
	}
	row.attempt.WorkID = row.work.ID
	if dispatched != nil {
		row.attempt.DispatchedAt = *dispatched
	}
	if reconciled != nil {
		row.attempt.ReconciledAt = *reconciled
	}
	return row, nil
}

func assignOwnedAncestorHeads(f *orders.SplitAncestorSettlementFacts, number string) {
	for _, head := range f.Lineage.CurrentHeads {
		if head.PostingNumber == number {
			f.Selected = head
		}
		if head.PostingNumber == f.NodeSplit.Before.PostingNumber {
			f.CurrentNode = head
		}
	}
}
