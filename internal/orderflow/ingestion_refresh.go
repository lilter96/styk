package orderflow

import (
	"context"
	"errors"
	"net"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func (i *Ingestor) readActivePage(ctx context.Context, scope orders.Scope, active orders.PostingPage) orders.OrderSourcePage {
	page := orders.OrderSourcePage{Postings: make([]orders.Posting, 0, len(active.Postings)), NextCursor: active.NextCursor, Complete: active.NextCursor == "", Exhausted: active.NextCursor == ""}
	for index, known := range active.Postings {
		posting, err := i.source.ReadOrderPosting(ctx, scope, known.PostingNumber)
		code, stop := refreshReadFailure(err)
		if err == nil && (posting.Scope != scope || posting.PostingNumber != known.PostingNumber) {
			code = "POSTING_SCOPE_MISMATCH"
		}
		if code == "" && posting.Validate() != nil {
			code = "POSTING_SCHEMA_DRIFT"
		}
		if code == "" {
			page.Postings = append(page.Postings, posting)
			continue
		}
		page.Failures = append(page.Failures, orders.ReadFailure{PostingNumber: known.PostingNumber, Code: code})
		page.Complete = false
		if stop {
			if index < len(active.Postings)-1 {
				page.NextCursor, page.Exhausted = known.PostingNumber, false
			}
			break
		}
	}
	return page
}

func refreshReadFailure(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var status interface{ ProviderReadStatus() int }
	if errors.As(err, &status) {
		return refreshStatusFailure(status.ProviderReadStatus())
	}
	var budget interface{ ProviderBudgetExhausted() bool }
	if errors.As(err, &budget) && budget.ProviderBudgetExhausted() {
		return "BUDGET_EXHAUSTED", true
	}
	var network net.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) {
		return "READ_INTERRUPTED", true
	}
	if errors.Is(err, providercall.ErrCredentialsRejected) || errors.Is(err, providercall.ErrMethodForbidden) {
		return "AUTHORIZATION_DENIED", true
	}
	if errors.Is(err, providercall.ErrLeaseLost) || errors.Is(err, providercall.ErrAlreadyStarted) || errors.Is(err, providercall.ErrCooldownUnbounded) {
		return "BUDGET_EXHAUSTED", true
	}
	if errors.Is(err, orders.ErrNotFound) {
		return "POSTING_NOT_FOUND", false
	}
	if errors.Is(err, orders.ErrSchemaDrift) {
		return "POSTING_SCHEMA_DRIFT", false
	}
	return "POSTING_READ_FAILED", false
}

func refreshStatusFailure(status int) (string, bool) {
	switch {
	case status == 401 || status == 403:
		return "AUTHORIZATION_DENIED", true
	case status == 429:
		return "RATE_LIMITED", true
	case status >= 500:
		return "PROVIDER_UNAVAILABLE", true
	case status == 404:
		return "POSTING_NOT_FOUND", false
	default:
		return "POSTING_READ_FAILED", false
	}
}

func retainRefreshFailures(page *orders.SourcePage, previous orders.Checkpoint) {
	if page.Stream != ActiveStream && page.Stream != orders.NativePointStream {
		return
	}
	var failures []orders.ReadFailure
	truncated := false
	if page.RefreshKey == previous.RefreshKey {
		failures, truncated = previous.Failures, previous.FailuresTruncated
	}
	if page.Stream == orders.NativePointStream {
		page.Failures, page.FailuresTruncated = orders.MergeNativePointFailures(failures, page.Failures, page.NativeReads, truncated)
	} else {
		page.Failures, page.FailuresTruncated = orders.MergeReadFailures(failures, page.Failures, page.Postings, truncated)
	}
	page.Complete = page.Complete && len(page.Failures) == 0 && !page.FailuresTruncated
}
