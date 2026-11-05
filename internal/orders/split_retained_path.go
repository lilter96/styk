package orders

import "slices"

// SplitRetainedGeneration uses the actual retained output at that generation.
// It may differ from today's head; it never pretends to be a fresh observation.
type SplitRetainedGeneration struct {
	Split           SplitLineageStep
	Origin          Work
	OriginTarget    TargetObservation
	Reduction       *Work
	ReductionTarget *TargetObservation
	Retained        Posting
}

func newRetainedHistorySettlement(f SplitAncestorSettlementFacts) (SplitAncestorSettlementPermission, error) {
	root, err := retainedHistoryRoot(f)
	if err != nil {
		return SplitAncestorSettlementPermission{}, err
	}
	history := f.RetainedHistory
	first := history[0]
	ancestors := []SplitAncestorNative{{Posting: minimalSupersededPosting(first.Retained), WorkID: first.Reduction.ID, Target: *first.ReductionTarget}}
	var previous SplitAncestorSettlementPermission
	for index := 1; index < len(history); index++ {
		generation := history[index]
		baseline, e := retainedHistoryOrigin(f, index, root, previous)
		if e != nil {
			return SplitAncestorSettlementPermission{}, e
		}
		p := retainedHistoryPermission(f, generation, baseline, ancestors)
		p, e = deepAncestorReduction(retainedGeneration(generation), p)
		if e != nil {
			return SplitAncestorSettlementPermission{}, e
		}
		previous, ancestors = p, p.Ancestors
	}
	selected, err := deepAncestorSelect(previous, retainedGeneration(history[len(history)-1]), f.Selected)
	if err != nil || !retainedHistoryCurrent(f, selected) {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	return freezeAncestorSettlement(selected)
}

func retainedGeneration(g SplitRetainedGeneration) SplitAncestorGeneration {
	return SplitAncestorGeneration{Split: g.Split, Origin: g.Origin, OriginTarget: g.OriginTarget, Reduction: g.Reduction, ReductionTarget: g.ReductionTarget, Current: g.Retained}
}

func retainedHistoryPermission(f SplitAncestorSettlementFacts, g SplitRetainedGeneration, baseline PositionBaseline, ancestors []SplitAncestorNative) SplitAncestorSettlementPermission {
	frontier := slices.Clone(ancestors)
	entry := SplitAncestorNative{Posting: minimalSupersededPosting(g.Retained), WorkID: g.Origin.ID, Target: g.OriginTarget}
	replaced := false
	for index, old := range frontier {
		if old.Posting.PostingNumber == g.Split.Before.PostingNumber {
			frontier[index], replaced = entry, true
		}
	}
	if !replaced {
		frontier = append(frontier, entry)
	}
	return SplitAncestorSettlementPermission{Scope: f.Selected.Scope, Mode: SplitAncestorReduce, SplitWorkID: g.Split.Work.ID, RootSplitWorkID: f.RetainedHistory[0].Split.Work.ID, NodeOriginWorkID: g.Origin.ID, Before: minimalSupersededPosting(g.Split.Before), Source: minimalSupersededPosting(g.Retained), Node: minimalSupersededPosting(g.Retained), Baseline: baseline, Ancestors: frontier}
}

func retainedHistoryOrigin(f SplitAncestorSettlementFacts, index int, root SplitReserveTransferPermission, previous SplitAncestorSettlementPermission) (PositionBaseline, error) {
	g, before := f.RetainedHistory[index], f.RetainedHistory[index-1]
	w, target := g.Origin, g.OriginTarget
	if !retainedOriginBound(g) {
		return PositionBaseline{}, ErrBlocked
	}
	if g.Split.Before.PostingNumber == before.Split.Before.PostingNumber {
		if before.Reduction == nil || before.ReductionTarget == nil || w.ID != before.Reduction.ID || !retainedNativeWorkEqual(w, target, *before.Reduction, *before.ReductionTarget) {
			return PositionBaseline{}, ErrBlocked
		}
	} else if !retainedHistoryChildOrigin(f, index, root, previous) {
		return PositionBaseline{}, ErrBlocked
	}
	baseline, err := ProjectionPositionBaseline(w, target)
	if err != nil || !splitTransferAllParentLinesReserved(g.Split.Before, baseline) {
		return PositionBaseline{}, ErrBlocked
	}
	return baseline, nil
}

func retainedOriginBound(g SplitRetainedGeneration) bool {
	w, before := g.Origin, g.Split.Before
	return ancestorNativeAcknowledged(w, g.OriginTarget) && w.Scope == before.Scope && w.Command.Scope == before.Scope && w.Command.PostingNumber == before.PostingNumber && w.Command.CommercialOrderID == before.CommercialOrderID && w.Command.ExpectedSourceRevision == before.Evidence.Revision
}

func retainedHistoryChildOrigin(f SplitAncestorSettlementFacts, index int, root SplitReserveTransferPermission, previous SplitAncestorSettlementPermission) bool {
	g := f.RetainedHistory[index]
	if g.Origin.Command.Action != ActionMirrorCreate {
		return false
	}
	if index == 1 {
		return g.Origin.Command.SplitTransfer != nil && g.Origin.Command.SplitTransfer.Equal(root)
	}
	expected, err := deepAncestorSelect(previous, retainedGeneration(f.RetainedHistory[index-1]), g.Split.Before)
	return err == nil && g.Origin.Command.SplitAncestorSettlement != nil && g.Origin.Command.SplitAncestorSettlement.Equal(expected)
}
