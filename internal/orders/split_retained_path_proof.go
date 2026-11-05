package orders

import (
	"reflect"
	"slices"
)

func retainedHistoryRoot(f SplitAncestorSettlementFacts) (SplitReserveTransferPermission, error) {
	if !f.HistoryUnambiguous || len(f.RetainedHistory) < 2 || len(f.RetainedHistory) > MaxSplitLineageDepth || !ancestorSettlementHeadsFresh(f.Lineage.CurrentHeads, f.CheckedAt) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil || !nestedSettlementAttempts(NestedSplitSettlementFacts{Lineage: f.Lineage, Attempts: f.Attempts, CheckedAt: f.CheckedAt}, graph) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	proof, err := NewSplitLineage(f.Lineage)
	if err != nil || !proof.CurrentMatches(f.Selected) || !retainedHistoryBound(f, proof) || !retainedHistoryClocks(f) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	first := f.RetainedHistory[0]
	baseline, err := ProjectionPositionBaseline(first.Origin, first.OriginTarget)
	if err != nil || first.Origin.Command.Action != ActionMirrorCreate || !retainedOriginBound(first) || first.Reduction == nil || first.ReductionTarget == nil || !ancestorNativeAcknowledged(*first.Reduction, *first.ReductionTarget) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	prior, err := OwnedSplitReductionBaseline(first.Reduction.Command)
	if err != nil || !baseline.Equal(prior) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	return retainedHistoryRootTransfer(f)
}

func retainedHistoryRootTransfer(f SplitAncestorSettlementFacts) (SplitReserveTransferPermission, error) {
	first, next := f.RetainedHistory[0], f.RetainedHistory[1]
	for _, output := range first.Split.Work.Receipt.ObservedPostings {
		if output.PostingNumber == first.Split.Before.PostingNumber {
			continue
		}
		child := output
		if next.Split.Before.PostingNumber != first.Split.Before.PostingNumber {
			if output.PostingNumber != next.Split.Before.PostingNumber {
				continue
			}
			child = next.Split.Before
		}
		proof, err := NewSplitReserveTransfer(SplitReserveTransferFacts{Split: first.Split.Work, Before: first.Split.Before, OriginalCorrelation: first.Split.Work.Command.Correlation, ParentRelease: *first.Reduction, Parent: first.Retained, ParentTarget: *first.ReductionTarget, Child: child})
		if err == nil {
			return proof, nil
		}
	}
	return SplitReserveTransferPermission{}, ErrBlocked
}

func retainedHistoryBound(f SplitAncestorSettlementFacts, proof SplitLineageProof) bool {
	ids := make([]string, 0, len(f.RetainedHistory))
	seen := make(map[string]bool)
	repeated := false
	for index, g := range f.RetainedHistory {
		ids = append(ids, g.Split.Work.ID)
		if !deepAncestorStepBound(f.Lineage, g.Split.Work, g.Split.Before) || !retainedHistoryNode(g) {
			return false
		}
		number := g.Split.Before.PostingNumber
		if seen[number] {
			if index == 0 || f.RetainedHistory[index-1].Split.Before.PostingNumber != number {
				return false
			}
			repeated = true
		}
		seen[number] = true
	}
	last := f.RetainedHistory[len(f.RetainedHistory)-1]
	return repeated && slices.Equal(ids, proof.PathWorkIDs()) && deepAncestorCurrent(f.Lineage, last.Retained) && splitTransferSameAllocation(ancestorNodeOutput(last.Split.Work, f.Selected.PostingNumber), f.Selected)
}

func retainedHistoryNode(g SplitRetainedGeneration) bool {
	w, before := g.Split.Work, g.Split.Before
	if w.Command.Action != ActionPostingSplitUnassembled || !unassembledAcknowledgedAllocation(w) || w.Command.Correlation != g.OriginTarget.Correlation || w.Command.ExpectedSourceRevision != before.Evidence.Revision || g.Retained.PostingNumber != before.PostingNumber || !splitTransferSameAllocation(g.Retained, ancestorNodeOutput(w, before.PostingNumber)) {
		return false
	}
	for _, output := range w.Receipt.ObservedPostings {
		if !splitTransferProductIdentities(before.Lines, output.Lines) {
			return false
		}
	}
	return true
}

func retainedNativeWorkEqual(a Work, at TargetObservation, b Work, bt TargetObservation) bool {
	return reflect.DeepEqual(a, b) && reflect.DeepEqual(at, bt)
}

func retainedHistoryCurrent(f SplitAncestorSettlementFacts, p SplitAncestorSettlementPermission) bool {
	for _, ancestor := range p.Ancestors {
		if !deepAncestorCurrent(f.Lineage, ancestor.Posting) {
			return false
		}
	}
	return true
}
