package orders

func (page SourcePage) validateNativeDocumentReads() error {
	if len(page.NativeDocumentReads) == 0 && len(page.NativeDocumentFailures) == 0 && !page.NativeDocumentFailuresTruncated {
		return nil
	}
	if page.Stream != NativePointStream || len(page.NativeDocumentReads) > MaxPagePostings || len(page.NativeDocumentFailures) > MaxPagePostings || page.Complete && (len(page.NativeDocumentFailures) > 0 || page.NativeDocumentFailuresTruncated) {
		return ErrInvalid
	}
	failures := make(map[string]NativeDocumentReadFailure, len(page.NativeDocumentFailures))
	for _, failure := range page.NativeDocumentFailures {
		if !validID(failure.WorkID) || !validID(failure.PostingNumber) || failures[failure.WorkID].WorkID != "" || validateNativePointFailureCode(failure.Code) != nil {
			return ErrInvalid
		}
		failures[failure.WorkID] = failure
	}
	seen := make(map[string]bool, len(page.NativeDocumentReads))
	for _, read := range page.NativeDocumentReads {
		if read.Validate() != nil || read.Parent.Scope != page.Scope || read.CredentialRevision != page.Credentials.Moysklad || read.StartedAt.Before(page.StartedAt) || read.FinishedAt.After(page.FinishedAt) || seen[read.WorkID] {
			return ErrInvalid
		}
		failure := failures[read.WorkID]
		if read.Code == "" && failure.WorkID != "" || read.Code != "" && (page.Complete || failure.Code != read.Code || failure.PostingNumber != read.Parent.PostingNumber) {
			return ErrInvalid
		}
		seen[read.WorkID] = true
	}
	return nil
}

func MergeNativeDocumentFailures(previous, current []NativeDocumentReadFailure, reads []NativeDocumentPointRead, truncated bool) ([]NativeDocumentReadFailure, bool) {
	success := make(map[string]bool, len(reads))
	for _, read := range reads {
		if read.Code == "" && read.Observation != nil {
			success[read.WorkID] = true
		}
	}
	result := make([]NativeDocumentReadFailure, 0, min(MaxPagePostings, len(previous)+len(current)))
	indexes := make(map[string]int, cap(result))
	for _, list := range [][]NativeDocumentReadFailure{previous, current} {
		for _, failure := range list {
			if success[failure.WorkID] {
				continue
			}
			if index, found := indexes[failure.WorkID]; found {
				result[index] = failure
				continue
			}
			if len(result) >= MaxPagePostings {
				truncated = true
				continue
			}
			indexes[failure.WorkID] = len(result)
			result = append(result, failure)
		}
	}
	return result, truncated
}

func (page *SourcePage) RetainNativeDocumentFailures(previous Checkpoint) {
	if page.Stream != NativePointStream {
		return
	}
	var failures []NativeDocumentReadFailure
	truncated := page.NativeDocumentFailuresTruncated
	if page.RefreshKey == previous.RefreshKey {
		failures = previous.NativeDocumentFailures
		truncated = truncated || previous.NativeDocumentFailuresTruncated
	}
	page.NativeDocumentFailures, page.NativeDocumentFailuresTruncated = MergeNativeDocumentFailures(failures, page.NativeDocumentFailures, page.NativeDocumentReads, truncated)
	page.Complete = page.Complete && len(page.NativeDocumentFailures) == 0 && !page.NativeDocumentFailuresTruncated
}
