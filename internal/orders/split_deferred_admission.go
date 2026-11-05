package orders

// The first native order may already contain a later split's retained
// allocation. Its actual creation cut selects that exact acknowledged receipt;
// earlier larger allocations are never fabricated as a native reserve.
func deferredOriginAfterSplit(f SplitAncestorSettlementFacts, index int) bool {
	d := f.Deferred
	if index < 0 || index >= len(d.Spine) {
		return false
	}
	o, first := d.Origin, d.Spine[index]
	splits, ok := deepAncestorTimes(f.Attempts, f.CheckedAt)
	if !ok {
		return false
	}
	native, ok := deepAncestorTimes(f.NativeAttempts, f.CheckedAt)
	if !ok {
		return false
	}
	return deferredOriginAdmission(f, first, native[o.Work.ID], splits[first.Work.ID])
}

func deferredOriginAdmission(f SplitAncestorSettlementFacts, first SplitLineageStep, origin, split NestedSplitAttempt) bool {
	o := f.Deferred.Origin
	for _, child := range first.Work.Receipt.ObservedPostings {
		if child.PostingNumber == first.Before.PostingNumber {
			continue
		}
		_, err := NewRetainedSplitAdmission(RetainedSplitAdmissionFacts{
			Split: first.Work, Before: first.Before, Parent: o.Source, Child: child,
			Children: first.Work.Receipt.ObservedPostings, AdmissionSource: o.Source,
			OriginalCorrelation: first.Work.Command.Correlation, Admission: o.Work, Target: o.Target,
			SplitReconciledAt: split.ReconciledAt, NativeCreatedAt: origin.CreatedAt,
			NativeDispatchedAt: origin.DispatchedAt, NativeReconciledAt: origin.ReconciledAt,
			// Sole at this historical origin: the complete root history is
			// origin plus separately validated later reductions, not absent rows.
			SoleNativeAdmission: true,
		})
		if err == nil {
			return true
		}
	}
	return false
}
