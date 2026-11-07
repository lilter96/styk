package orderflow

import (
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// AutomaticNativeProposal accepts the configured seller's full-posting physical
// declaration. Position ownership, prior consumption and fresh provider facts
// are still checked by the existing native planner and dispatcher.
func AutomaticNativeProposal(review orders.ReviewContext, posting orders.Posting, intent orders.MerchantIntent) (orders.NativeMovementProposal, orders.Validation) {
	proposal := orders.NativeMovementProposal{PostingNumber: posting.PostingNumber, Action: intent.Action, Applicable: orders.Present(true), EventConfirmed: true}
	if review.Config.Lifecycle == nil || intent.Validate() != nil || intent.Scope != posting.Scope || intent.Correlation.PostingNumber != posting.PostingNumber || intent.ConfigRevision != review.Config.Revision || intent.CredentialRevision != review.Credentials.Moysklad || !intent.WholePosting || !automaticNativeScheme(review, posting, intent) {
		return proposal, nativeAutomaticIssue("native_automatic_physical_intent_required")
	}
	policy := review.Config.Lifecycle
	if _, found := policy.Mapping(intent); !found || !policy.Active(intent.ObservedAt, posting.Scope, review.Config.Revision) {
		return proposal, nativeAutomaticIssue("native_automatic_policy_required")
	}
	var err error
	proposal.EventOccurredAt, err = orders.NativeParentVersionTime(intent.AfterUpdatedAt)
	if err != nil {
		return proposal, nativeAutomaticIssue("native_physical_event_time_unverified")
	}
	proposal, issue := automaticNativeProposalEvent(review, posting, intent, proposal)
	if issue.Code != "" {
		return proposal, issue
	}
	return automaticNativeProposalLines(proposal, posting)
}

func automaticNativeScheme(review orders.ReviewContext, posting orders.Posting, intent orders.MerchantIntent) bool {
	if posting.Scheme.IsKnown() {
		return *posting.Scheme.Value == intent.Scheme
	}
	warehouse, issue := postingWarehouse(posting, review)
	profile := warehouse.Capability
	return issue.Code == "" && profile.Validate() == nil && profile.Scheme == intent.Scheme && posting.DeliveryMethodID.IsKnown() && profile.DeliveryMethodID == *posting.DeliveryMethodID.Value && profile.WarehouseID == *posting.WarehouseID.Value && slices.Contains(profile.VerifiedDimensions, "scheme") && slices.Contains(profile.VerifiedDimensions, "warehouse") && !intent.ObservedAt.Before(profile.ObservedAt) && intent.ObservedAt.Before(profile.ValidUntil)
}

func automaticNativeProposalEvent(review orders.ReviewContext, posting orders.Posting, intent orders.MerchantIntent, proposal orders.NativeMovementProposal) (orders.NativeMovementProposal, orders.Validation) {
	if !orders.AutomaticNativeSourceSupported(posting) {
		return proposal, nativeAutomaticIssue("native_source_status_unverified")
	}
	policy := review.Config.Lifecycle
	proposal.EventKind, proposal.EventReference = "shipment_dispatched", "moysklad-physical-v1:"+intent.Digest
	if intent.Action == orders.ActionDemandCreate {
		if !policy.AutoDemand || policy.DemandMoment != orders.OnHandoffConfirmed || intent.EventKind != "handoff_confirmed" {
			return proposal, nativeAutomaticIssue("native_shipment_evidence_required")
		}
		if !posting.Status.IsKnown() || slices.Contains([]string{"cancelled", "cancelled_from_split_pending", "not_accepted", "arbitration", "client_arbitration"}, *posting.Status.Value) {
			return proposal, nativeAutomaticIssue("native_shipment_source_exception_requires_review")
		}
	} else if intent.Action == orders.ActionSalesReturnCreate && policy.AutoReturn && intent.EventKind == "return_received" {
		proposal.EventKind, proposal.DemandWorkID = "return_received", automaticNativeDemandBasis(review, intent)
		if proposal.DemandWorkID == "" {
			return proposal, nativeAutomaticIssue("native_unique_applied_demand_required")
		}
	} else {
		return proposal, nativeAutomaticIssue("native_physical_event_action_mismatch")
	}
	return proposal, orders.Validation{}
}

func automaticNativeProposalLines(proposal orders.NativeMovementProposal, posting orders.Posting) (orders.NativeMovementProposal, orders.Validation) {
	if len(posting.Lines) == 0 || len(posting.Lines) > 100 {
		return proposal, nativeAutomaticIssue("native_automatic_whole_posting_unverified")
	}
	proposal.Lines = make([]orders.ActionQuantity, 0, len(posting.Lines))
	for _, line := range posting.Lines {
		if line.Identity.Validate() != nil || line.Quantity.Validate() != nil || line.CancelledQuantity.IsSpecified() && (!line.CancelledQuantity.IsKnown() || !orders.ExactDecimalEqual(*line.CancelledQuantity.Value, "0")) {
			return proposal, nativeAutomaticIssue("native_automatic_partial_movement_requires_review")
		}
		proposal.Lines = append(proposal.Lines, orders.ActionQuantity{Identity: line.Identity, Quantity: line.Quantity})
	}
	return proposal, orders.Validation{}
}

func automaticNativeDemandBasis(review orders.ReviewContext, intent orders.MerchantIntent) string {
	bindings := automaticNativeDemandBindings(review, intent)
	result := ""
	for _, binding := range bindings {
		actual := binding.Observation
		if binding.PostingNumber != intent.Correlation.PostingNumber || actual.Scope != intent.Scope || actual.Correlation.Kind != "demand" || !actual.CustomerOrderID.IsKnown() || *actual.CustomerOrderID.Value != intent.Correlation.CustomerOrderID || !actual.Applicable.IsKnown() || !*actual.Applicable.Value {
			continue
		}
		if actual.Validate() != nil || result != "" || binding.WorkID == "" {
			return ""
		}
		result = binding.WorkID
	}
	return result
}

func automaticNativeDemandBindings(review orders.ReviewContext, intent orders.MerchantIntent) map[string]orders.NativeDocumentBinding {
	if len(review.NativeDocuments) != 0 {
		return review.NativeDocuments
	}
	facts := review.NativeConsumption[intent.Correlation.PostingNumber]
	if !facts.Complete || facts.Scope != intent.Scope || facts.Correlation != intent.Correlation {
		return nil
	}
	bindings := make(map[string]orders.NativeDocumentBinding, len(facts.Events))
	for _, event := range facts.Events {
		work := event.Work
		if work.State != orders.StateReconciled || work.Command.Action != orders.ActionDemandCreate || work.Command.Correlation != intent.Correlation || event.Document == nil || event.Parent == nil || !orders.NativeDocumentMatches(work.Command, *event.Document) {
			continue
		}
		bindings[work.ID] = orders.NativeDocumentBinding{WorkID: work.ID, PostingNumber: work.Command.PostingNumber, Observation: *event.Document, Parent: *event.Parent}
	}
	return bindings
}

func automaticNativeProposalMatches(review orders.ReviewContext, posting orders.Posting, proposal orders.NativeMovementProposal, intent *orders.MerchantIntent) bool {
	if intent == nil {
		return false
	}
	expected, issue := AutomaticNativeProposal(review, posting, *intent)
	return issue.Code == "" && proposal.RetryWorkID == "" && proposal.PostingNumber == expected.PostingNumber && proposal.Action == expected.Action && proposal.Applicable.IsKnown() && *proposal.Applicable.Value && proposal.EventKind == expected.EventKind && proposal.EventConfirmed && proposal.EventOccurredAt.Equal(expected.EventOccurredAt) && proposal.EventReference == expected.EventReference && proposal.DemandWorkID == expected.DemandWorkID && slices.Equal(proposal.Lines, expected.Lines)
}

func nativeAutomaticIssue(code string) orders.Validation {
	return orders.Validation{Field: "native_documents.automation", Code: code}
}

func nativeAutomaticIntent(intents []*orders.MerchantIntent) *orders.MerchantIntent {
	if len(intents) != 1 {
		return nil
	}
	return intents[0]
}

func nativeProposalPolicyAllows(review orders.ReviewContext, posting orders.Posting, proposal orders.NativeMovementProposal, automatic bool, intent *orders.MerchantIntent, now time.Time) bool {
	policy := review.Config.NativeDocuments
	if policy == nil || !proposal.Applicable.IsKnown() {
		return false
	}
	if !automatic {
		return intent == nil && policy.Allows(proposal.Action, *proposal.Applicable.Value, now)
	}
	return policy.Enabled && policy.Validate() == nil && automaticNativeProposalMatches(review, posting, proposal, intent)
}

func nativeCommandPolicyAllows(config orders.Config, command orders.Command, now time.Time) bool {
	if command.MerchantIntent != nil {
		return command.NativeMovement.AllowedByLifecycle(config, command, now)
	}
	return command.NativeMovement.AllowedBy(config.NativeDocuments, command.Action, now)
}
