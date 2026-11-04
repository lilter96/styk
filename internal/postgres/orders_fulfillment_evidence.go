package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func validateFulfillmentQuantityEvidence(ctx context.Context, q ordersQuerier, work orders.Work, completion orders.Completion) error {
	if completion.State != orders.StateReconciled || (work.Command.Action != orders.ActionPostingPartialCancel && work.Command.Action != orders.ActionPostingSplit) {
		return nil
	}
	if work.Command.PostingAction == nil || (work.Command.Action == orders.ActionPostingPartialCancel && completion.ObservedPosting == nil) {
		return orders.ErrInvalid
	}
	preview, err := readOrderPreview(ctx, q, work.Scope, work.PreviewID)
	if err != nil {
		return err
	}
	for _, decision := range preview.Decisions {
		if decision.Posting.PostingNumber != work.Command.PostingNumber || len(decision.Commands) != 1 {
			continue
		}
		hash, err := orders.CommandFingerprint(decision.Commands[0])
		if err != nil || hash != work.CommandFingerprint {
			return orders.ErrStale
		}
		if work.Command.Action == orders.ActionPostingSplit {
			if !splitObservationsFollowAttempt(work, completion.Receipt) {
				return orders.ErrStale
			}
			if !orders.SplitObserved(decision.Posting, work.Command.PostingAction.Parameters.Packages, work.Receipt.PostingNumbers, completion.Receipt.ObservedPostings) {
				return orders.ErrStale
			}
			return nil
		}
		if !orders.PartialCancellationObserved(decision.Posting, *completion.ObservedPosting, work.Command.PostingAction.Parameters.Lines) {
			return orders.ErrStale
		}
		return nil
	}
	return orders.ErrNotFound
}

func splitObservationsFollowAttempt(work orders.Work, receipt orders.Receipt) bool {
	attempted := work.Receipt.ObservedAt
	if attempted.IsZero() || !receipt.ObservedAt.Equal(attempted) || receipt.HTTPStatus != work.Receipt.HTTPStatus {
		return false
	}
	for _, child := range receipt.ObservedPostings {
		checked := child.Evidence.CheckedAt
		if checked.IsZero() {
			checked = child.Evidence.ObservedAt
		}
		if checked.Before(attempted) {
			return false
		}
	}
	return true
}
