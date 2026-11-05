package orders

// Historical root reductions need the same actual split, reviewed allocation,
// native ownership and acknowledgement as a transfer. Partial scalar progress
// is not itself a transfer: callers still compare the complete actual frontier
// before admitting any child.
func stagedLegacySplitReduction(f SplitReserveTransferFacts) bool {
	if !splitTransferRootVerified(f) || !splitTransferAllocationVerified(f) {
		return false
	}
	w, target := f.ParentRelease, f.ParentTarget
	c := w.Command
	prior, err := OwnedSplitReductionBaseline(c)
	if err != nil || !ancestorNativeAcknowledged(w, target) || c.Correlation != f.OriginalCorrelation || c.RetainedSplitWorkID != f.Split.ID || c.ExpectedSourceRevision != f.Parent.Evidence.Revision || c.CommercialOrderID != f.Parent.CommercialOrderID || c.PostingNumber != f.Parent.PostingNumber || !OwnedSplitReductionObserved(c, target) {
		return false
	}
	p := SplitRetainedReservePermission{Scope: f.Before.Scope, SplitWorkID: f.Split.ID, Before: f.Before, Correlation: f.OriginalCorrelation}
	return p.ValidateWrite(f.Parent, *c.CustomerOrder, prior) == nil && stagedSplitReductionProgress(w, target, prior)
}
