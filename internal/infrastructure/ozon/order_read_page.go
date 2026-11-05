package ozon

import (
	"context"
	"errors"
	"net"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func orderDetailPageKeys(data []byte, cursor string, limit int) (orders.OrderSourcePage, []string, error) {
	fields := newOrderFields(data)
	rows := fields.array("postings")
	more, next := fields.boolean("has_next"), fields.text("cursor")
	page := orders.OrderSourcePage{Postings: make([]orders.Posting, 0, len(rows))}
	if *fields.err != nil || rows == nil || !more.IsKnown() || len(rows) > limit {
		return page, nil, errOrderPayload
	}
	page.Exhausted, page.Complete = !*more.Value, !*more.Value
	if !page.Exhausted {
		if !next.IsKnown() || *next.Value == cursor || len(rows) == 0 {
			return page, nil, errOrderPayload
		}
		page.NextCursor = *next.Value
	}
	seen := make(map[string]bool, len(rows))
	numbers := make([]string, 0, len(rows))
	for _, row := range rows {
		number := orderKnown(row.text("posting_number"))
		if *fields.err != nil || !unassembledPostingID(number) || seen[number] {
			return page, nil, errOrderPayload
		}
		seen[number] = true
		numbers = append(numbers, number)
	}
	return page, numbers, nil
}

func orderPageDetailFailure(err error) (string, bool) {
	var status interface{ ProviderReadStatus() int }
	if errors.As(err, &status) {
		return orderPageStatusFailure(status.ProviderReadStatus())
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
	if providercall.IsNotDispatched(err) || errors.Is(err, orders.ErrBlocked) || errors.Is(err, orders.ErrStale) {
		return "READ_INTERRUPTED", true
	}
	if errors.Is(err, errOrderPayload) || errors.Is(err, orders.ErrSchemaDrift) {
		return "POSTING_SCHEMA_DRIFT", false
	}
	if errors.Is(err, orders.ErrNotFound) {
		return "POSTING_NOT_FOUND", false
	}
	return "POSTING_READ_FAILED", false
}

func orderPageStatusFailure(status int) (string, bool) {
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

// This marker is emitted only after a valid discovery list identified every
// current-page key. It never classifies failures of the list endpoint itself.
type orderDetailPhaseError struct{ cause error }

func (e orderDetailPhaseError) Error() string                { return "Ozon posting detail phase stopped" }
func (e orderDetailPhaseError) Unwrap() error                { return e.cause }
func (e orderDetailPhaseError) OzonDetailPhaseStopped() bool { return true }
