package orders

func forestNativeValid(f SplitForestFacts, e SplitForestNative) bool {
	previous, found := forestPrevious(f, e.Source.PostingNumber, e.Attempt.CreatedAt, e.Work.ID)
	if e.Work.Command.Action == ActionMirrorCreate {
		return !found && forestCreation(f, e)
	}
	prior, invalid := OwnedSplitReductionBaseline(e.Work.Command)
	if !found || invalid != nil || !OwnedSplitReductionObserved(e.Work.Command, e.Target) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(previous.Work, previous.Target)
	if err != nil || !prior.Equal(baseline) {
		return false
	}
	step, ok := forestSplit(f, retainedNativeEventSplit(e.Work.Command))
	if !ok || step.Before.PostingNumber != e.Source.PostingNumber || !forestSplitBefore(f, step, e) || !splitTransferSameAllocation(forestPrefix(f, e.Source.PostingNumber, e.Attempt.CreatedAt), e.Source) {
		return false
	}
	if p := e.Work.Command.SplitAncestorSettlement; p != nil {
		return p.Mode == SplitAncestorReduce && forestGrant(f, e, *p) && p.AllowsCommand(e.Work.Command)
	}
	return forestLegacyReduction(f, e, step)
}

func retainedNativeEventSplit(c Command) string {
	if c.SplitAncestorSettlement != nil {
		return c.SplitAncestorSettlement.SplitWorkID
	}
	return c.RetainedSplitWorkID
}

func forestSplitBefore(f SplitForestFacts, step SplitLineageStep, e SplitForestNative) bool {
	a, found := forestAttempt(f, step.Work.ID)
	return found && !a.ReconciledAt.After(e.Attempt.CreatedAt)
}

func forestRoot(f SplitForestFacts) SplitLineageStep {
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil {
		return SplitLineageStep{}
	}
	return f.Lineage.Splits[graph.root]
}

func forestCreation(f SplitForestFacts, e SplitForestNative) bool {
	baseline, err := ProjectionPositionBaseline(e.Work, e.Target)
	if err != nil || !splitTransferAllParentLinesReserved(e.Source, baseline) {
		return false
	}
	if e.Source.PostingNumber == f.Lineage.RootPosting {
		return forestRootCreation(f, e)
	}
	step, found := forestBirth(f, e.Source.PostingNumber)
	if !found || !forestSplitBefore(f, step, e) || !splitTransferSameAllocation(forestPrefix(f, e.Source.PostingNumber, e.Attempt.CreatedAt), e.Source) || !splitTransferSameAllocation(ancestorNodeOutput(step.Work, e.Source.PostingNumber), e.Source) {
		return false
	}
	if p := e.Work.Command.SplitAncestorSettlement; p != nil {
		return p.Mode == SplitAncestorAdmit && forestGrant(f, e, *p) && p.AllowsCommand(e.Work.Command)
	}
	return forestTransferredCreation(f, e, step)
}

func forestRootCreation(f SplitForestFacts, e SplitForestNative) bool {
	first := forestRoot(f)
	a, found := forestAttempt(f, first.Work.ID)
	if !found {
		return false
	}
	if !e.Attempt.ReconciledAt.After(a.CreatedAt) {
		return splitTransferPostingEqual(e.Source, first.Before) && e.Target.Correlation == first.Work.Command.Correlation
	}
	for _, child := range first.Work.Receipt.ObservedPostings {
		if child.PostingNumber != first.Before.PostingNumber && forestFirstAdmission(f, e, first, child) {
			return true
		}
	}
	return false
}

func forestFirstAdmission(f SplitForestFacts, e SplitForestNative, step SplitLineageStep, child Posting) bool {
	a, ok := forestAttempt(f, step.Work.ID)
	if !ok || !splitTransferSameAllocation(forestPrefix(f, e.Source.PostingNumber, e.Attempt.CreatedAt), e.Source) {
		return false
	}
	_, err := NewRetainedSplitAdmission(RetainedSplitAdmissionFacts{Split: step.Work, Before: step.Before, Parent: e.Source, Child: child, Children: step.Work.Receipt.ObservedPostings, AdmissionSource: e.Source, OriginalCorrelation: step.Work.Command.Correlation, Admission: e.Work, Target: e.Target, SplitReconciledAt: a.ReconciledAt, NativeCreatedAt: e.Attempt.CreatedAt, NativeDispatchedAt: e.Attempt.DispatchedAt, NativeReconciledAt: e.Attempt.ReconciledAt, SoleNativeAdmission: true})
	return err == nil
}

func forestLegacyReduction(f SplitForestFacts, e SplitForestNative, step SplitLineageStep) bool {
	for _, child := range step.Work.Receipt.ObservedPostings {
		if child.PostingNumber == step.Before.PostingNumber {
			continue
		}
		_, err := NewSplitReserveTransfer(SplitReserveTransferFacts{Split: step.Work, Before: step.Before, OriginalCorrelation: step.Work.Command.Correlation, ParentRelease: e.Work, Parent: e.Source, ParentTarget: e.Target, Child: child})
		if err == nil {
			return true
		}
	}
	return false
}
