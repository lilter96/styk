package orders

import (
	"reflect"
	"slices"
)

// SplitAncestorGeneration binds one non-root split to its exact native history.
// The store supplies a complete path; absent reduction is not a zero reserve.
type SplitAncestorGeneration struct {
	Split           SplitLineageStep
	Origin          Work
	OriginTarget    TargetObservation
	Reduction       *Work
	ReductionTarget *TargetObservation
	Current         Posting
}

func newDeepAncestorSettlement(f SplitAncestorSettlementFacts) (SplitAncestorSettlementPermission, error) {
	root, err := deepAncestorRoot(f)
	if err != nil {
		return SplitAncestorSettlementPermission{}, err
	}
	ancestors := []SplitAncestorNative{{Posting: minimalSupersededPosting(f.RootTransfer.Parent), WorkID: f.RootTransfer.ParentRelease.ID, Target: f.RootTransfer.ParentTarget}}
	var previous SplitAncestorSettlementPermission
	for i, generation := range f.Path {
		baseline, e := deepAncestorOrigin(f, i, root, previous)
		if e != nil {
			return SplitAncestorSettlementPermission{}, e
		}
		p := deepAncestorPermission(f, generation, baseline, ancestors)
		p, e = deepAncestorReduction(generation, p)
		if e != nil {
			return SplitAncestorSettlementPermission{}, e
		}
		previous, ancestors = p, p.Ancestors
	}
	last := f.Path[len(f.Path)-1]
	selected, err := deepAncestorSelect(previous, last, f.Selected)
	if err != nil {
		return SplitAncestorSettlementPermission{}, err
	}
	return freezeAncestorSettlement(selected)
}

func deepAncestorRoot(f SplitAncestorSettlementFacts) (SplitReserveTransferPermission, error) {
	if !f.HistoryUnambiguous || len(f.Path)+1 > MaxSplitLineageDepth || len(f.Path) < 2 || !ancestorSettlementHeadsFresh(f.Lineage.CurrentHeads, f.CheckedAt) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil || !nestedSettlementAttempts(NestedSplitSettlementFacts{Lineage: f.Lineage, Attempts: f.Attempts, CheckedAt: f.CheckedAt}, graph) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	proof, err := NewSplitLineage(f.Lineage)
	if err != nil || !proof.CurrentMatches(f.Selected) || !deepAncestorPathBound(f, proof) || !deepAncestorChronology(f) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	baseline, err := ProjectionPositionBaseline(f.RootOrigin, f.RootOriginTarget)
	if err != nil || !ancestorNativeAcknowledged(f.RootOrigin, f.RootOriginTarget) || f.RootTransfer.ParentRelease.Command.PositionPatch == nil || !baseline.Equal(f.RootTransfer.ParentRelease.Command.PositionPatch.Baseline) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	if !splitTransferPostingEqual(f.RootTransfer.Child, f.Path[0].Split.Before) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	return NewSplitReserveTransfer(f.RootTransfer)
}

func deepAncestorPathBound(f SplitAncestorSettlementFacts, proof SplitLineageProof) bool {
	ids := []string{f.RootTransfer.Split.ID}
	if !deepAncestorStepBound(f.Lineage, f.RootTransfer.Split, f.RootTransfer.Before) || !deepAncestorCurrent(f.Lineage, f.RootTransfer.Parent) {
		return false
	}
	parent := f.RootTransfer.Before.PostingNumber
	for _, generation := range f.Path {
		ids = append(ids, generation.Split.Work.ID)
		if !deepAncestorStepBound(f.Lineage, generation.Split.Work, generation.Split.Before) || !deepAncestorCurrent(f.Lineage, generation.Current) || !deepAncestorNode(generation, parent) {
			return false
		}
		parent = generation.Split.Before.PostingNumber
	}
	output := ancestorNodeOutput(f.Path[len(f.Path)-1].Split.Work, f.Selected.PostingNumber)
	return slices.Equal(ids, proof.PathWorkIDs()) && splitTransferSameAllocation(output, f.Selected)
}

func deepAncestorStepBound(lineage SplitLineageFacts, work Work, before Posting) bool {
	for _, step := range lineage.Splits {
		if step.Work.ID == work.ID {
			return reflect.DeepEqual(step.Work, work) && splitTransferPostingEqual(step.Before, before)
		}
	}
	return false
}

func deepAncestorCurrent(lineage SplitLineageFacts, p Posting) bool {
	for _, head := range lineage.CurrentHeads {
		if head.PostingNumber == p.PostingNumber {
			return splitTransferPostingEqual(head, p)
		}
	}
	return false
}

func deepAncestorNode(g SplitAncestorGeneration, parent string) bool {
	w, before := g.Split.Work, g.Split.Before
	if w.Command.Action != ActionPostingSplitUnassembled || !unassembledAcknowledgedAllocation(w) || w.Command.Correlation != g.OriginTarget.Correlation || w.Command.ExpectedSourceRevision != before.Evidence.Revision || !ancestorParentConsistent(before, parent) || g.Current.PostingNumber != before.PostingNumber || !splitTransferSameAllocation(g.Current, ancestorNodeOutput(w, before.PostingNumber)) {
		return false
	}
	for _, output := range w.Receipt.ObservedPostings {
		if !splitTransferProductIdentities(before.Lines, output.Lines) {
			return false
		}
	}
	return true
}

func deepAncestorOrigin(f SplitAncestorSettlementFacts, index int, root SplitReserveTransferPermission, previous SplitAncestorSettlementPermission) (PositionBaseline, error) {
	g := f.Path[index]
	w, t := g.Origin, g.OriginTarget
	if w.Command.Action != ActionMirrorCreate || !ancestorNativeAcknowledged(w, t) || w.Command.PostingNumber != g.Split.Before.PostingNumber || w.Command.ExpectedSourceRevision != g.Split.Before.Evidence.Revision {
		return PositionBaseline{}, ErrBlocked
	}
	if index == 0 {
		if w.Command.SplitTransfer == nil || !w.Command.SplitTransfer.Equal(root) {
			return PositionBaseline{}, ErrBlocked
		}
	} else {
		expected, err := deepAncestorSelect(previous, f.Path[index-1], g.Split.Before)
		if err != nil || w.Command.SplitAncestorSettlement == nil || !w.Command.SplitAncestorSettlement.Equal(expected) {
			return PositionBaseline{}, ErrBlocked
		}
	}
	baseline, err := ProjectionPositionBaseline(w, t)
	if err != nil || !splitTransferAllParentLinesReserved(g.Split.Before, baseline) {
		return PositionBaseline{}, ErrBlocked
	}
	return baseline, nil
}

func deepAncestorPermission(f SplitAncestorSettlementFacts, g SplitAncestorGeneration, baseline PositionBaseline, ancestors []SplitAncestorNative) SplitAncestorSettlementPermission {
	frontier := slices.Clone(ancestors)
	frontier = append(frontier, SplitAncestorNative{Posting: minimalSupersededPosting(g.Current), WorkID: g.Origin.ID, Target: g.OriginTarget})
	return SplitAncestorSettlementPermission{Scope: f.Selected.Scope, Mode: SplitAncestorReduce, SplitWorkID: g.Split.Work.ID, RootSplitWorkID: f.RootTransfer.Split.ID, NodeOriginWorkID: g.Origin.ID, Before: minimalSupersededPosting(g.Split.Before), Source: minimalSupersededPosting(g.Current), Node: minimalSupersededPosting(g.Current), Baseline: baseline, Ancestors: frontier}
}

func deepAncestorReduction(g SplitAncestorGeneration, p SplitAncestorSettlementPermission) (SplitAncestorSettlementPermission, error) {
	if g.Reduction == nil {
		if g.ReductionTarget != nil {
			return p, ErrBlocked
		}
		return p, nil
	}
	facts := SplitAncestorSettlementFacts{NodeReduction: g.Reduction, NodeReductionTarget: g.ReductionTarget, CurrentNode: g.Current}
	if ancestorSettlementReduced(facts, p) != nil {
		return p, ErrBlocked
	}
	baseline, err := ProjectionPositionBaseline(*g.Reduction, *g.ReductionTarget)
	if err != nil {
		return p, ErrBlocked
	}
	p.Baseline, p.NodeReductionWorkID = baseline, g.Reduction.ID
	last := len(p.Ancestors) - 1
	p.Ancestors[last].WorkID, p.Ancestors[last].Target = g.Reduction.ID, *g.ReductionTarget
	return p, nil
}

func deepAncestorSelect(p SplitAncestorSettlementPermission, g SplitAncestorGeneration, selected Posting) (SplitAncestorSettlementPermission, error) {
	if selected.PostingNumber != g.Current.PostingNumber {
		if g.Reduction == nil || !splitTransferChildReservable(selected) || !ancestorParentConsistent(selected, g.Current.PostingNumber) || !splitTransferSameAllocation(ancestorNodeOutput(g.Split.Work, selected.PostingNumber), selected) {
			return p, ErrBlocked
		}
		p.Mode, p.Source = SplitAncestorAdmit, minimalSupersededPosting(selected)
	}
	if p.Validate() != nil {
		return p, ErrBlocked
	}
	return p, nil
}
