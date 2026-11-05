package orders

func deferredReduction(f SplitAncestorSettlementFacts, prior, next SplitDeferredNative) bool {
	w := next.Work
	priorBaseline, invalid := OwnedSplitReductionBaseline(w.Command)
	if !deferredNativeBound(f, next) || invalid != nil || !OwnedSplitReductionObserved(w.Command, next.Target) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(prior.Work, prior.Target)
	if err != nil || !priorBaseline.Equal(baseline) || !stagedSplitReductionProgress(w, next.Target, baseline) {
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
	return true
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
		if stagedLegacySplitReduction(SplitReserveTransferFacts{Split: step.Work, Before: step.Before,
			OriginalCorrelation: f.Deferred.Origin.Target.Correlation, ParentRelease: n.Work,
			Parent: n.Source, ParentTarget: n.Target, Child: output}) {
			return true
		}
	}
	return false
}
