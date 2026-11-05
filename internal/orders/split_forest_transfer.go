package orders

func forestTransferredCreation(f SplitForestFacts, e SplitForestNative, step SplitLineageStep) bool {
	p := e.Work.Command.SplitTransfer
	if p == nil || p.SplitWorkID != step.Work.ID || !p.Allows(e.Source) {
		return false
	}
	parent, found := forestPrevious(f, step.Before.PostingNumber, e.Attempt.CreatedAt, e.Work.ID)
	if !found || parent.Work.ID != p.ParentWorkID || !splitTransferSameAllocation(forestPrefix(f, step.Before.PostingNumber, e.Attempt.CreatedAt), p.ParentPosting) {
		return false
	}
	facts := SplitReserveTransferFacts{Split: step.Work, Before: step.Before, OriginalCorrelation: step.Work.Command.Correlation, ParentRelease: parent.Work, Parent: p.ParentPosting, ParentTarget: parent.Target, Child: e.Source}
	expected, err := NewSplitReserveTransfer(facts)
	if err == nil && expected.Equal(*p) {
		return true
	}
	if parent.Work.Command.Action != ActionMirrorCreate {
		return false
	}
	a, exists := forestAttempt(f, step.Work.ID)
	if !exists {
		return false
	}
	expected, err = NewRetainedSplitAdmission(RetainedSplitAdmissionFacts{Split: step.Work, Before: step.Before, Parent: p.ParentPosting, Child: e.Source, Children: step.Work.Receipt.ObservedPostings, AdmissionSource: parent.Source, OriginalCorrelation: step.Work.Command.Correlation, Admission: parent.Work, Target: parent.Target, SplitReconciledAt: a.ReconciledAt, NativeCreatedAt: parent.Attempt.CreatedAt, NativeDispatchedAt: parent.Attempt.DispatchedAt, NativeReconciledAt: parent.Attempt.ReconciledAt, SoleNativeAdmission: true})
	return err == nil && expected.Equal(*p)
}
