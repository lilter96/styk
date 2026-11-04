package orderflow

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Reads are a durable serial traversal after the acknowledged attempt, not an
// atomic provider snapshot. Each child retains its actual observation time.
func (r *Runner) readbackSplit(ctx context.Context, work orders.Work, client orders.Provider) error {
	decision, err := r.reviewedDecision(ctx, work)
	if err != nil || work.Command.PostingAction == nil || len(work.Receipt.PostingNumbers) < 2 {
		return r.deferReadback(ctx, work, "SPLIT_LINEAGE_REQUIRES_INVESTIGATION")
	}
	receipt := work.Receipt
	receipt.ObservedPostings = eligibleSplitObservations(receipt.ObservedPostings, receipt.ObservedAt, r.now())
	reads := 0
	for _, number := range receipt.PostingNumbers {
		if slices.ContainsFunc(receipt.ObservedPostings, func(p orders.Posting) bool { return p.PostingNumber == number }) {
			continue
		}
		if reads >= 10 {
			break
		}
		posting, readErr := client.ReadOrderPosting(ctx, work.Scope, number)
		reads++
		if readErr != nil {
			break
		}
		if posting.Scope != work.Scope || posting.PostingNumber != number || posting.CommercialOrderID != work.Command.CommercialOrderID {
			return r.deferReadback(ctx, work, "SPLIT_CHILD_IDENTITY_MISMATCH")
		}
		posting.PII = nil
		if orders.SplitChildObserved(decision.Posting, posting, work.Command.PostingAction.Parameters.Packages) {
			receipt.ObservedPostings = append(receipt.ObservedPostings, posting)
		}
	}
	state, code := orders.StatePolling, "SPLIT_READBACK_PENDING"
	if orders.SplitObserved(decision.Posting, work.Command.PostingAction.Parameters.Packages, receipt.PostingNumbers, receipt.ObservedPostings) {
		state, code = orders.StateReconciled, "SPLIT_CHILDREN_CONFIRMED"
	}
	if work.State == orders.StateUnknown && state != orders.StateReconciled {
		state = orders.StateUnknown
	}
	receipt.State = state
	return r.complete(ctx, work, orders.Completion{State: state, Code: code, Receipt: receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func eligibleSplitObservations(postings []orders.Posting, attempted, now time.Time) []orders.Posting {
	result := make([]orders.Posting, 0, len(postings))
	for _, posting := range postings {
		checked := posting.Evidence.CheckedAt
		if checked.IsZero() {
			checked = posting.Evidence.ObservedAt
		}
		if !checked.After(now) && !checked.Before(attempted) {
			result = append(result, posting)
		}
	}
	return result
}
