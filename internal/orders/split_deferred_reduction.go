package orders

func deferredReduction(f SplitAncestorSettlementFacts, prior, next SplitDeferredNative) bool {
	w := next.Work
	if !deferredNativeBound(f, next) || w.Command.Action != ActionMirrorPositionUpdate || w.Command.PositionPatch == nil || !PositionPatchObserved(w.Command, next.Target) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(prior.Work, prior.Target)
	if err != nil || !w.Command.PositionPatch.Baseline.Equal(baseline) {
		return false
	}
	step, found := deferredReductionStep(f, w)
	if !found || !splitTransferSameAllocation(ancestorNodeOutput(step.Work, next.Source.PostingNumber), next.Source) {
		return false
	}
	if w.Command.SplitAncestorSettlement == nil {
		return deferredLegacyReduction(f, next, step)
	}
	expected, err := deferredPermission(f, prior, next.Source, step)
	if err != nil {
		return false
	}
	actual := *w.Command.SplitAncestorSettlement
	if splitTransferPostingEqual(actual.Before, step.Before) {
		expected.Before = minimalSupersededPosting(step.Before)
	}
	if !actual.Equal(expected) || !actual.AllowsCommand(w.Command) {
		return false
	}
	current, err := ProjectionPositionBaseline(w, next.Target)
	return err == nil && splitTransferAllParentLinesReserved(next.Source, current)
}

func deferredReductionStep(f SplitAncestorSettlementFacts, w Work) (SplitLineageStep, bool) {
	id := w.Command.RetainedSplitWorkID
	if w.Command.SplitAncestorSettlement != nil {
		id = w.Command.SplitAncestorSettlement.SplitWorkID
	}
	for _, step := range f.Deferred.Spine {
		if step.Work.ID == id {
			return step, true
		}
	}
	return SplitLineageStep{}, false
}

func deferredLegacyReduction(f SplitAncestorSettlementFacts, n SplitDeferredNative, step SplitLineageStep) bool {
	for _, output := range step.Work.Receipt.ObservedPostings {
		if output.PostingNumber == step.Before.PostingNumber {
			continue
		}
		_, err := NewSplitReserveTransfer(SplitReserveTransferFacts{Split: step.Work, Before: step.Before,
			OriginalCorrelation: f.Deferred.Origin.Target.Correlation, ParentRelease: n.Work,
			Parent: n.Source, ParentTarget: n.Target, Child: output})
		if err == nil {
			return true
		}
	}
	return false
}
