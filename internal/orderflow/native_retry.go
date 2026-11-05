package orderflow

import (
	"context"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

// Retry proposals select an earlier attempt, not a new physical event. Restore
// its protected reference server-side without returning it in the preview.
func (s *Service) nativeRetryProposal(ctx context.Context, review orders.ReviewContext, proposal orders.NativeMovementProposal) (orders.NativeMovementProposal, orders.Validation) {
	if proposal.RetryWorkID == "" {
		return proposal, orders.Validation{}
	}
	prior, found := nativeRetryPrior(review, proposal)
	if !found {
		return proposal, nativeRetryIssue("native_retry_prior_rejection_unavailable")
	}
	reader, ok := s.options.ActionProtector.(orders.ActionInputReader)
	if !ok {
		return proposal, nativeRetryIssue("native_retry_protected_event_unavailable")
	}
	movement := prior.Command.NativeMovement
	input, err := reader.ReadOrderActionInput(ctx, prior.Scope, prior.Command.PostingNumber, prior.Command.Action, movement.Event.ProtectedInput)
	if err != nil || input.EventReference == "" || input.NativeEventKind != movement.Event.Kind || input.EventConfirmed != movement.Event.Confirmed || !input.NativeEventOccurredAt.Equal(movement.Event.OccurredAt) {
		return proposal, nativeRetryIssue("native_retry_protected_event_unavailable")
	}
	return restoreNativeRetryEvent(proposal, prior.Command, input)
}

func nativeRetryPrior(review orders.ReviewContext, proposal orders.NativeMovementProposal) (orders.Work, bool) {
	facts, found := review.NativeConsumption[proposal.PostingNumber]
	if !found || !facts.Complete || facts.Scope != review.Config.Scope || facts.Correlation.PostingNumber != proposal.PostingNumber {
		return orders.Work{}, false
	}
	var prior orders.Work
	for _, event := range facts.Events {
		work := event.Work
		if work.ID != proposal.RetryWorkID {
			continue
		}
		if prior.ID != "" || !ordermoney.NativeRetryCandidate(event) || work.Scope != review.Config.Scope || work.Command.Scope != work.Scope || work.Command.Correlation != facts.Correlation || work.Command.PostingNumber != proposal.PostingNumber || work.Command.Action != proposal.Action || work.Command.NativeMovement == nil || work.Command.Validate() != nil {
			return orders.Work{}, false
		}
		prior = work
	}
	return prior, prior.ID != ""
}

func restoreNativeRetryEvent(proposal orders.NativeMovementProposal, prior orders.Command, input orders.ActionParameters) (orders.NativeMovementProposal, orders.Validation) {
	movement := prior.NativeMovement
	if strings.TrimSpace(proposal.EventReference) != "" && strings.TrimSpace(proposal.EventReference) != input.EventReference || proposal.EventKind != "" && proposal.EventKind != movement.Event.Kind || !proposal.Applicable.IsKnown() || !movement.Applicable.IsKnown() || *proposal.Applicable.Value != *movement.Applicable.Value {
		return proposal, nativeRetryIssue("native_retry_event_identity_changed")
	}
	if *proposal.Applicable.Value && (!proposal.EventConfirmed || !proposal.EventOccurredAt.IsZero() && !proposal.EventOccurredAt.Equal(movement.Event.OccurredAt)) {
		return proposal, nativeRetryIssue("native_retry_physical_event_confirmation_required")
	}
	if !*proposal.Applicable.Value && proposal.EventConfirmed {
		return proposal, nativeRetryIssue("native_retry_event_identity_changed")
	}
	proposal.EventReference, proposal.EventKind = input.EventReference, movement.Event.Kind
	proposal.EventOccurredAt = time.Time{}
	if *proposal.Applicable.Value {
		proposal.EventOccurredAt = movement.Event.OccurredAt
	}
	if proposal.Action == orders.ActionSalesReturnCreate && proposal.DemandWorkID == "" {
		proposal.DemandWorkID = movement.DemandWorkID
	}
	return proposal, orders.Validation{}
}

func sealNativeConsumption(command orders.Command, review orders.ReviewContext, proposal orders.NativeMovementProposal) orders.Validation {
	facts, found := review.NativeConsumption[command.PostingNumber]
	movement := command.NativeMovement
	if proposal.RetryWorkID != "" {
		retry, invalid := ordermoney.PlanNativeRetry(command, facts, proposal.RetryWorkID)
		if invalid.Code != "" {
			return invalid
		}
		movement.Retry = retry
	}
	if !found && movement.ParentEffect != orders.NativeParentUnpostedDocument && movement.Retry == nil {
		return orders.Validation{}
	}
	seal, invalid := ordermoney.PlanNativeConsumption(command, facts)
	if invalid.Code == "" {
		movement.Consumption = &seal
	}
	return invalid
}

func nativeRetryIssue(code string) orders.Validation {
	return orders.Validation{Field: "native_documents.retry", Code: code}
}
