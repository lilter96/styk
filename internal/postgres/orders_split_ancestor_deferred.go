package postgres

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/orders"
)

var errDeferredOwnedUnsafe = errors.New("deferred owned history is unresolved")

func blockDeferredOwnedTarget(result *orders.ReviewContext, number string) {
	delete(result.SplitAncestorSettlements, number)
	delete(result.SplitRetainedReserves, number)
	delete(result.SplitReserveTransfers, number)
	delete(result.SplitReservePermissions, number)
	delete(result.SplitSupersededReserves, number)
	if result.SplitReserveBlocked == nil {
		result.SplitReserveBlocked = make(map[string]bool)
	}
	result.SplitReserveBlocked[number] = true
}

func deferredOwnedSpine(lineage orders.SplitLineageFacts, number string) ([]orders.SplitLineageStep, bool) {
	lineage.SelectedPosting = lineage.RootPosting
	proof, err := orders.NewSplitLineage(lineage)
	if err != nil {
		return nil, false
	}
	ids := proof.PathWorkIDs()
	if len(ids) < 2 || len(ids) > orders.MaxSplitLineageDepth || len(ids) != len(lineage.Splits) {
		return nil, false
	}
	byID := make(map[string]orders.SplitLineageStep, len(lineage.Splits))
	for _, step := range lineage.Splits {
		byID[step.Work.ID] = step
	}
	spine := make([]orders.SplitLineageStep, 0, len(ids))
	selected := number == lineage.RootPosting
	for _, id := range ids {
		step := byID[id]
		if step.Before.PostingNumber != lineage.RootPosting || !deferredRetainsRoot(step) {
			return nil, false
		}
		spine = append(spine, step)
		for _, output := range step.Work.Receipt.ObservedPostings {
			selected = selected || output.PostingNumber == number
		}
	}
	return spine, selected
}

func deferredRetainsRoot(step orders.SplitLineageStep) bool {
	for _, output := range step.Work.Receipt.ObservedPostings {
		if output.PostingNumber == step.Before.PostingNumber {
			return true
		}
	}
	return false
}

func loadDeferredOwnedSettlement(ctx context.Context, q ordersQuerier, nested orders.NestedSplitSettlementFacts, number, selfID, fingerprint string) (orders.SplitAncestorSettlementFacts, error) {
	f := orders.SplitAncestorSettlementFacts{Lineage: nested.Lineage, Attempts: nested.Attempts}
	f.Lineage.SelectedPosting = number
	spine, applicable := deferredOwnedSpine(f.Lineage, number)
	if !applicable {
		return f, orders.ErrBlocked
	}
	last := spine[len(spine)-1]
	f.Deferred = &orders.SplitDeferredSettlementFacts{Spine: spine}
	assignDeferredOwnedHeads(&f, number, last.Before.PostingNumber)
	if number != last.Before.PostingNumber && !orders.DeferredChildReservable(f.Selected) {
		return orders.SplitAncestorSettlementFacts{}, orders.ErrBlocked
	}
	rows, err := readDeferredOwnedRows(ctx, q, f.Lineage.Scope, spine, selfID, fingerprint)
	if err != nil {
		return f, err
	}
	if len(rows) == 0 {
		return f, orders.ErrBlocked // No native history: preserve native-free workflows.
	}
	if !assignDeferredOwnedRows(&f, rows) {
		return f, errDeferredOwnedUnsafe
	}
	if err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&f.CheckedAt); err != nil {
		return f, ordersRowsError("read deferred settlement clock", err)
	}
	if !orders.DeferredOriginChronologySupported(f) {
		return orders.SplitAncestorSettlementFacts{}, orders.ErrBlocked
	}
	f.HistoryUnambiguous = true
	if _, err = orders.NewSplitAncestorSettlement(f); err != nil {
		return f, errDeferredOwnedUnsafe
	}
	return f, nil
}

func assignDeferredOwnedHeads(f *orders.SplitAncestorSettlementFacts, selected, root string) {
	for _, head := range f.Lineage.CurrentHeads {
		if head.PostingNumber == selected {
			f.Selected = head
		}
		if head.PostingNumber == root {
			f.Deferred.Current = head
			f.CurrentNode = head
		}
	}
}

func deferredOwnedMappingsCurrent(f *orders.SplitDeferredSettlementFacts, current *orders.ReviewContext) bool {
	works := []orders.Work{f.Origin.Work}
	for _, reduction := range f.Reductions {
		works = append(works, reduction.Work)
	}
	for _, work := range works {
		if work.Command.CustomerOrder == nil || !retainedAdmissionMappingsCurrent(orders.RetainedSplitAdmissionFacts{Admission: work}, current) {
			return false
		}
	}
	return true
}
