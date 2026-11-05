package orders

import (
	"slices"
	"time"
)

// NestedSplitNativeHistory is an exact scoped database traversal, not a claim
// that MoySklad has no unrelated orders. Non-nil empty arrays distinguish a
// completed zero-result check from missing evidence.
type NestedSplitNativeHistory struct {
	Scope         Scope
	PostingNumber string
	BindingIDs    []string
	// MirrorWorkIDs is a bounded actual existence witness, not an exhaustive audit list.
	MirrorWorkIDs []string
}

type NestedSplitAttempt struct {
	WorkID                                string
	CreatedAt, DispatchedAt, ReconciledAt time.Time
}

// This first settlement policy covers only a completely unmirrored ancestor
// chain. Native-owned ancestors need a separate actual reserve frontier proof.
type NestedSplitSettlementFacts struct {
	Lineage             SplitLineageFacts
	SelectedSplitWorkID string
	NativeHistory       []NestedSplitNativeHistory
	Attempts            []NestedSplitAttempt
	CheckedAt           time.Time
}

func NewNestedSplitSettlement(f NestedSplitSettlementFacts, child Posting) (SplitReservePermission, error) {
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil || len(graph.path) < 2 || f.CheckedAt.IsZero() || !graphCurrentChild(graph, child) {
		return SplitReservePermission{}, ErrBlocked
	}
	if !nestedSettlementAttempts(f, graph) || !nestedSettlementNatives(f) || !nestedSettlementSources(f) {
		return SplitReservePermission{}, ErrBlocked
	}
	selected := graph.facts.Splits[graph.selectedStep()]
	if selected.Work.ID != f.SelectedSplitWorkID {
		return SplitReservePermission{}, ErrBlocked
	}
	permission := SplitReservePermission{Scope: f.Lineage.Scope, SplitWorkID: selected.Work.ID, ParentPostingNumber: selected.Before.PostingNumber, PostingNumber: child.PostingNumber}
	if !permission.Allows(child) || !splitTransferChildReservable(child) {
		return SplitReservePermission{}, ErrBlocked
	}
	return permission, nil
}

func graphCurrentChild(graph splitLineageGraph, child Posting) bool {
	current, found := graph.heads[child.PostingNumber]
	return found && graph.facts.SelectedPosting == child.PostingNumber && slices.ContainsFunc(graph.frontier, func(p Posting) bool { return p.PostingNumber == child.PostingNumber }) && splitTransferPostingEqual(minimalSupersededPosting(current), minimalSupersededPosting(child))
}

func nestedSettlementSources(f NestedSplitSettlementFacts) bool {
	for _, step := range f.Lineage.Splits {
		w := step.Work
		if w.Command.Action != ActionPostingSplitUnassembled || w.Command.Correlation.CustomerOrderID != "" || !unassembledAcknowledgedAllocation(w) {
			return false
		}
	}
	for _, posting := range f.Lineage.CurrentHeads {
		if knownLineageRetired(posting) && (posting.Times.DeliveringAt.IsKnown() || posting.Times.DeliveredAt.IsKnown() || reservationAfterShipment(posting.Cancellation)) {
			return false
		}
		checked := posting.Evidence.CheckedAt
		if checked.IsZero() {
			checked = posting.Evidence.ObservedAt
		}
		if checked.IsZero() || checked.Before(f.CheckedAt.Add(-30*time.Second)) || checked.After(f.CheckedAt.Add(5*time.Second)) {
			return false
		}
	}
	return true
}

func nestedSettlementNatives(f NestedSplitSettlementFacts) bool {
	wanted := make(map[string]bool, len(f.Lineage.Splits))
	for _, step := range f.Lineage.Splits {
		wanted[step.Before.PostingNumber] = true
	}
	if len(wanted) != len(f.NativeHistory) {
		return false
	}
	for _, history := range f.NativeHistory {
		if !wanted[history.PostingNumber] || history.Scope != f.Lineage.Scope || history.BindingIDs == nil || history.MirrorWorkIDs == nil || len(history.BindingIDs) > 0 || len(history.MirrorWorkIDs) > 0 {
			return false
		}
		delete(wanted, history.PostingNumber)
	}
	return len(wanted) == 0
}

func nestedSettlementAttempts(f NestedSplitSettlementFacts, graph splitLineageGraph) bool {
	if len(f.Attempts) != len(f.Lineage.Splits) {
		return false
	}
	attempts := make(map[string]NestedSplitAttempt, len(f.Attempts))
	for _, attempt := range f.Attempts {
		if _, duplicate := attempts[attempt.WorkID]; duplicate || attempt.CreatedAt.IsZero() || attempt.DispatchedAt.IsZero() || attempt.ReconciledAt.IsZero() || attempt.DispatchedAt.Before(attempt.CreatedAt) || attempt.ReconciledAt.Before(attempt.DispatchedAt) || attempt.ReconciledAt.After(f.CheckedAt.Add(5*time.Second)) {
			return false
		}
		attempts[attempt.WorkID] = attempt
	}
	for index, step := range f.Lineage.Splits {
		attempt, found := attempts[step.Work.ID]
		if !found {
			return false
		}
		if parent := graph.parents[index]; parent >= 0 && attempt.CreatedAt.Before(attempts[f.Lineage.Splits[parent].Work.ID].ReconciledAt) {
			return false
		}
	}
	return true
}
