package orders

// ReadFailure contains only a requested posting identity and a local code.
// Provider errors, response bodies, addresses and credentials never belong here.
type ReadFailure struct {
	PostingNumber string `json:"posting_number"`
	Code          string `json:"code"`
}

func (page SourcePage) validateReadFailures(success map[string]bool) error {
	if len(page.Failures) > MaxPagePostings || (page.Complete && (len(page.Failures) > 0 || page.FailuresTruncated)) {
		return ErrInvalid
	}
	if page.Exhausted && page.NextCursor != "" {
		return ErrInvalid
	}
	if (page.Exhausted || len(page.Failures) > 0 || page.FailuresTruncated) && ((page.Stream != "ozon-orders-active" && page.Stream != NativePointStream) || !page.FreshDetail) {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(page.Failures))
	for _, failure := range page.Failures {
		if !validID(failure.PostingNumber) || seen[failure.PostingNumber] || success[failure.PostingNumber] || !page.validReadFailureCode(failure.Code) {
			return ErrInvalid
		}
		seen[failure.PostingNumber] = true
	}
	return nil
}

func (page SourcePage) validReadFailureCode(code string) bool {
	if page.Stream == NativePointStream {
		return validateNativePointFailureCode(code) == nil
	}
	return validReadFailureCode(code)
}

func validReadFailureCode(code string) bool {
	switch code {
	case "POSTING_NOT_FOUND", "POSTING_SCHEMA_DRIFT", "POSTING_SCOPE_MISMATCH", "POSTING_READ_FAILED",
		"AUTHORIZATION_DENIED", "RATE_LIMITED", "PROVIDER_UNAVAILABLE", "BUDGET_EXHAUSTED", "READ_INTERRUPTED":
		return true
	default:
		return false
	}
}

// MergeReadFailures retains bounded epoch diagnostics. Overflow remains explicit
// and prevents claiming complete coverage until a new traversal succeeds.
func MergeReadFailures(previous, current []ReadFailure, postings []Posting, truncated bool) ([]ReadFailure, bool) {
	success := make(map[string]bool, len(postings))
	for _, posting := range postings {
		success[posting.PostingNumber] = true
	}
	return mergeReadFailureKeys(previous, current, success, truncated)
}

func MergeNativePointFailures(previous, current []ReadFailure, reads []NativeOrderPointRead, truncated bool) ([]ReadFailure, bool) {
	success := make(map[string]bool, len(reads))
	for _, read := range reads {
		if read.Point != nil && read.Code == "" {
			success[read.Correlation.PostingNumber] = true
		}
	}
	return mergeReadFailureKeys(previous, current, success, truncated)
}

func mergeReadFailureKeys(previous, current []ReadFailure, success map[string]bool, truncated bool) ([]ReadFailure, bool) {
	failures := make([]ReadFailure, 0, min(MaxPagePostings, len(previous)+len(current)))
	indexes := make(map[string]int, cap(failures))
	for _, list := range [][]ReadFailure{previous, current} {
		for _, failure := range list {
			if success[failure.PostingNumber] {
				continue
			}
			if index, ok := indexes[failure.PostingNumber]; ok {
				failures[index] = failure
				continue
			}
			if len(failures) == MaxPagePostings {
				truncated = true
				continue
			}
			indexes[failure.PostingNumber] = len(failures)
			failures = append(failures, failure)
		}
	}
	return failures, truncated
}
