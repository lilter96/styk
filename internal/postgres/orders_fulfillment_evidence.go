package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func validatePartialCancellationEvidence(ctx context.Context, q ordersQuerier, work orders.Work, completion orders.Completion) error {
	if completion.State != orders.StateReconciled || work.Command.Action != orders.ActionPostingPartialCancel {
		return nil
	}
	if work.Command.PostingAction == nil || completion.ObservedPosting == nil {
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
		if !orders.PartialCancellationObserved(decision.Posting, *completion.ObservedPosting, work.Command.PostingAction.Parameters.Lines) {
			return orders.ErrStale
		}
		return nil
	}
	return orders.ErrNotFound
}
