package orders

// ParentSourceMatches compares current provider facts, not traversal clocks or
// opaque local revision labels. No buyer data is needed to transfer a reserve.
func (permission SplitReserveTransferPermission) ParentSourceMatches(posting Posting) bool {
	before := splitTransferSafePosting(permission.ParentPosting)
	current := splitTransferSafePosting(posting)
	left, err := PostingFingerprint(before)
	if err != nil {
		return false
	}
	right, err := PostingFingerprint(current)
	return err == nil && left == right
}

// ParentTargetMatches requires the entire native position frontier, including
// foreign reserves, to remain exactly as observed after the owned reduction.
func (permission SplitReserveTransferPermission) ParentTargetMatches(target TargetObservation) bool {
	if target.SourceDeleted.Validate() != nil || target.SourceDeleted.IsKnown() && *target.SourceDeleted.Value != "" {
		return false
	}
	return target.Scope == permission.Scope && target.Complete && target.Correlation == permission.ParentCorrelation && target.Revision == permission.ParentTargetRevision && target.Fields.StoreID == permission.ParentStoreID && target.Fields.OrganizationID == permission.ParentOrganizationID && target.Fields.Applicable.IsKnown() && *target.Fields.Applicable.Value && NativePositionsEqual(permission.ParentPositions, target.Positions)
}
