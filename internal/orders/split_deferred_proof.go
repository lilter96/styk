package orders

import "slices"

func deferredStructure(f SplitAncestorSettlementFacts) bool {
	d := f.Deferred
	if d == nil || !f.HistoryUnambiguous || len(d.Spine) < 2 || len(d.Spine) > MaxSplitLineageDepth || len(d.Spine) != len(f.Lineage.Splits) || len(d.Reductions) > len(d.Spine) || !ancestorSettlementHeadsFresh(f.Lineage.CurrentHeads, f.CheckedAt) {
		return false
	}
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil || !nestedSettlementAttempts(NestedSplitSettlementFacts{Lineage: f.Lineage, Attempts: f.Attempts, CheckedAt: f.CheckedAt}, graph) {
		return false
	}
	proof, err := NewSplitLineage(f.Lineage)
	if err != nil || !proof.CurrentMatches(f.Selected) || !deepAncestorCurrent(f.Lineage, d.Current) {
		return false
	}
	root := d.Spine[0].Before.PostingNumber
	ids := make([]string, 0, len(d.Spine))
	for _, step := range d.Spine {
		if step.Before.PostingNumber != root || step.Work.Command.Action != ActionPostingSplitUnassembled || !unassembledAcknowledgedAllocation(step.Work) || !deepAncestorStepBound(f.Lineage, step.Work, step.Before) {
			return false
		}
		ids = append(ids, step.Work.ID)
	}
	lineage := f.Lineage
	lineage.SelectedPosting = root
	rootProof, err := NewSplitLineage(lineage)
	if err != nil || !slices.Equal(ids, rootProof.PathWorkIDs()) || d.Current.PostingNumber != root {
		return false
	}
	return deferredOrigin(f) && deferredSelected(f)
}

func deferredSelected(f SplitAncestorSettlementFacts) bool {
	d := f.Deferred
	if f.Selected.PostingNumber == d.Current.PostingNumber {
		return splitTransferPostingEqual(f.Selected, d.Current)
	}
	for _, step := range d.Spine {
		output := ancestorNodeOutput(step.Work, f.Selected.PostingNumber)
		if output.PostingNumber != "" && splitTransferSameAllocation(output, f.Selected) {
			return ancestorParentConsistent(f.Selected, d.Current.PostingNumber)
		}
	}
	return false
}

func deferredOrigin(f SplitAncestorSettlementFacts) bool {
	d := f.Deferred
	o, first := d.Origin, d.Spine[0]
	if o.Work.Command.Action != ActionMirrorCreate || !deferredNativeBound(f, o) {
		return false
	}
	if deferredOriginCut(f) == deferredOriginPostFirstSplit {
		return deferredOriginAfterFirst(f)
	}
	if deferredOriginCut(f) != deferredOriginPreSplit || !splitTransferPostingEqual(o.Source, first.Before) || o.Target.Correlation != first.Work.Command.Correlation {
		return false
	}
	baseline, err := ProjectionPositionBaseline(o.Work, o.Target)
	return err == nil && splitTransferAllParentLinesReserved(o.Source, baseline)
}

func deferredNativeBound(f SplitAncestorSettlementFacts, n SplitDeferredNative) bool {
	root := f.Deferred.Spine[0].Before
	w, source := n.Work, n.Source
	return ancestorNativeAcknowledged(w, n.Target) && w.Scope == f.Lineage.Scope && w.Command.Scope == f.Lineage.Scope && lineagePostingTrustworthy(source) && source.Scope == f.Lineage.Scope && source.PostingNumber == root.PostingNumber && source.CommercialOrderID == root.CommercialOrderID && w.Command.PostingNumber == root.PostingNumber && w.Command.CommercialOrderID == root.CommercialOrderID && w.Command.ExpectedSourceRevision == source.Evidence.Revision && n.Target.Correlation == f.Deferred.Origin.Target.Correlation && baselineCorrelationMatches(w.Command.Correlation, n.Target.Correlation)
}
