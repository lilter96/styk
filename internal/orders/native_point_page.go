package orders

func (page SourcePage) validateNativePointReads() error {
	if err := page.validateNativeDocumentReads(); err != nil {
		return err
	}
	if IsNativeReadStream(page.Stream) && (len(page.Postings) > 0 || len(page.Secrets) > 0) {
		return ErrInvalid
	}
	if len(page.NativeReads) == 0 {
		return nil
	}
	if !IsNativeReadStream(page.Stream) || len(page.Postings) > 0 || len(page.Secrets) > 0 || len(page.NativeReads) > MaxPagePostings {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(page.NativeReads))
	failures := make(map[string]string, len(page.Failures))
	for _, failure := range page.Failures {
		failures[failure.PostingNumber] = failure.Code
	}
	for _, read := range page.NativeReads {
		if read.Validate() != nil || read.Correlation.Scope != page.Scope || read.CredentialRevision != page.Credentials.Moysklad || read.StartedAt.Before(page.StartedAt) || read.FinishedAt.After(page.FinishedAt) || seen[read.Correlation.PostingNumber] {
			return ErrInvalid
		}
		if read.Point != nil && failures[read.Correlation.PostingNumber] != "" || read.Point == nil && (page.Complete || failures[read.Correlation.PostingNumber] != read.Code) {
			return ErrInvalid
		}
		seen[read.Correlation.PostingNumber] = true
	}
	return nil
}
