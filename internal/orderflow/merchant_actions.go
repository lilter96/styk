package orderflow

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func automaticMerchantAction(review orders.ReviewContext, posting orders.Posting, intent orders.MerchantIntent) (orders.ActionProposal, orders.Validation) {
	proposal := orders.ActionProposal{PostingNumber: posting.PostingNumber, Action: intent.Action}
	policy := review.Config.Lifecycle
	warehouse, issue := postingWarehouse(posting, review)
	if policy == nil || intent.Validate() != nil || intent.Scope != posting.Scope || intent.ConfigRevision != review.Config.Revision || intent.CredentialRevision != review.Credentials.Moysklad || issue.Code != "" || warehouse.Capability.Scheme != intent.Scheme {
		return proposal, orders.Validation{Code: "merchant_scope_or_scheme_changed"}
	}
	if _, found := policy.Mapping(intent); !found || posting.AvailableActions == nil {
		return proposal, orders.Validation{Code: "merchant_action_capability_or_mapping_unverified"}
	}
	if intent.Action == orders.ActionPostingPack {
		return automaticMerchantPackage(proposal, posting, intent)
	}
	if slices.Contains([]orders.Action{orders.ActionPostingShip, orders.ActionPostingLastMile, orders.ActionPostingDeliver}, intent.Action) {
		proposal.Parameters.EventConfirmed, proposal.Parameters.EventReference = true, "moysklad-transition-v1:"+intent.Digest
		return proposal, orders.Validation{}
	}
	return proposal, orders.Validation{Code: "merchant_action_requires_reviewed_parameters"}
}

func automaticMerchantPackage(proposal orders.ActionProposal, posting orders.Posting, intent orders.MerchantIntent) (orders.ActionProposal, orders.Validation) {
	if !intent.WholePosting || intent.EventKind != "assembly_ready" || len(posting.Lines) == 0 || len(posting.Lines) > 100 {
		return proposal, orders.Validation{Code: "merchant_package_composition_required"}
	}
	lines := make([]orders.ActionQuantity, 0, len(posting.Lines))
	for _, line := range posting.Lines {
		if line.Identity.Validate() != nil || line.Quantity.Validate() != nil || line.CancelledQuantity.IsSpecified() && (!line.CancelledQuantity.IsKnown() || !orders.ExactDecimalEqual(*line.CancelledQuantity.Value, "0")) {
			return proposal, orders.Validation{Code: "merchant_partial_composition_requires_review"}
		}
		lines = append(lines, orders.ActionQuantity{Identity: line.Identity, Quantity: line.Quantity})
	}
	proposal.Parameters.Packages = []orders.ActionPackage{{Lines: lines}}
	proposal.Parameters.ReadinessConfirmed = true
	return proposal, orders.Validation{}
}

func (s *Service) planMerchantAction(ctx context.Context, request orders.PreviewRequest, review orders.ReviewContext, posting orders.Posting, proposal orders.ActionProposal, automatic bool, now time.Time) (orders.Decision, error) {
	intent := request.MerchantIntent
	if intent == nil {
		return s.planAction(ctx, review, posting, proposal, now)
	}
	blocked := orders.Decision{Posting: posting, Commands: []orders.Command{}, Validation: []orders.Validation{{Code: "merchant_native_transition_changed"}}}
	if !automatic || intent.Validate() != nil || review.Config.Lifecycle == nil || intent.Action != proposal.Action {
		return blocked, nil
	}
	target, err := s.readReverseTarget(ctx, review, intent.Correlation)
	if err != nil {
		return orders.Decision{}, err
	}
	if !merchantTargetMatches(*intent, target) {
		return blocked, nil
	}
	if proposal.Action == orders.ActionPostingPack && !merchantPackingPositionsMatch(review, posting, target) {
		blocked.Validation[0].Code = "merchant_packing_owned_positions_changed"
		return blocked, nil
	}
	decision, err := s.planAction(ctx, review, posting, proposal, now)
	if err != nil || !decision.Ready || len(decision.Commands) != 1 {
		return decision, err
	}
	command := &decision.Commands[0]
	command.MerchantIntent, command.LifecyclePolicyRevision, command.ExpectedTargetRevision = intent, review.Config.Lifecycle.Revision, intent.AfterRevision
	if command.Action == orders.ActionPostingPack {
		baseline := review.PositionBaselines[posting.PostingNumber]
		command.MerchantPositionBaseline = &baseline
	}
	if !review.Config.Lifecycle.Allows(*command, now) {
		return blocked, nil
	}
	safe := orders.NativeParentFacts(target)
	decision.Target = &safe
	return decision, nil
}

func merchantPackingPositionsMatch(review orders.ReviewContext, posting orders.Posting, target orders.TargetObservation) bool {
	baseline, found := review.PositionBaselines[posting.PostingNumber]
	if !found || baseline.Correlation != target.Correlation {
		return false
	}
	projected, issues := ordermoney.ProjectLines(posting, review.ProductMappings)
	if len(issues) != 0 || len(projected) != len(baseline.Bindings) {
		return false
	}
	for _, position := range projected {
		matched := false
		for _, binding := range baseline.Bindings {
			if binding.Source.Identity != position.Identity || !orders.ExactDecimalEqual(binding.Source.Quantity, position.Quantity) {
				continue
			}
			for _, current := range target.Positions {
				if current.ID == binding.Native.ID && orders.PositionRepresented(position, current) && current.Reserve.IsKnown() && current.Shipped.IsKnown() && orders.ExactDecimalEqual(*current.Shipped.Value, "0") && orders.SameObservedDecimal(binding.Native.Reserve, current.Reserve) {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func merchantTargetMatches(intent orders.MerchantIntent, target orders.TargetObservation) bool {
	return target.Complete && target.Scope == intent.Scope && target.Correlation == intent.Correlation && target.Revision == intent.AfterRevision && target.Fields.StateID == intent.AfterStateID && target.ProviderUpdatedAt.IsKnown() && *target.ProviderUpdatedAt.Value == intent.AfterUpdatedAt && !target.SourceDeleted.IsKnown()
}
