package orders

// CanRetryKnownUnsent excludes native identity conflicts even when STYK did not
// send a POST. A new preview marker cannot resolve the earlier native document.
func (work CommissionWork) CanRetryKnownUnsent() bool {
	if work.DispatchCount != 0 || work.State != StateBlocked && work.State != StateRejected {
		return false
	}
	switch work.Code {
	case "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW", "FINANCE_EXISTING_DOCUMENT_UNVERIFIED":
		return false
	}
	return true
}
