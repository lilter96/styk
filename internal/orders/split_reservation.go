package orders

// SplitReservePermission refers to an actual reconciled local action/readback.
// It proves absence of a prior STYK parent projection, not absence of unrelated
// merchant obligations or a provider-asserted parent relationship.
type SplitReservePermission struct {
	Scope               Scope  `json:"scope"`
	SplitWorkID         string `json:"split_work_id"`
	ParentPostingNumber string `json:"parent_posting_number"`
	PostingNumber       string `json:"posting_number"`
}

func (permission SplitReservePermission) Allows(posting Posting) bool {
	if permission.Scope.Validate() != nil || permission.Scope != posting.Scope || !validID(permission.SplitWorkID) || !validID(permission.ParentPostingNumber) || permission.PostingNumber != posting.PostingNumber || permission.ParentPostingNumber == posting.PostingNumber || len(posting.ChildPostingNumbers) > 0 || posting.ParentPostingNumber.Validate() != nil {
		return false
	}
	return !posting.ParentPostingNumber.IsKnown() || *posting.ParentPostingNumber.Value == permission.ParentPostingNumber
}

func ApplySplitOpenQuantityReserve(posting Posting, write *CustomerOrderWrite, baseline PositionBaseline, permission SplitReservePermission) Validation {
	if !permission.Allows(posting) {
		return reservePolicyValidation("posting.lineage", "reservation_policy_split_lineage_requires_review")
	}
	return applyOpenQuantityReserve(posting, write, baseline, true)
}
