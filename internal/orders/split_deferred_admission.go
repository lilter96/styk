package orders

// The first native order may already contain only the original split's retained
// allocation. Reuse its actual admission proof; never invent the pre-split reserve.
func deferredOriginAfterFirst(f SplitAncestorSettlementFacts) bool {
	d := f.Deferred
	o, first := d.Origin, d.Spine[0]
	splits, ok := deepAncestorTimes(f.Attempts, f.CheckedAt)
	if !ok {
		return false
	}
	native, ok := deepAncestorTimes(f.NativeAttempts, f.CheckedAt)
	if !ok {
		return false
	}
	origin, split := native[o.Work.ID], splits[first.Work.ID]
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
