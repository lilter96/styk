package orderflow

import (
	"context"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
	"slices"
	"time"
)

func validateCompositionProposals(request orders.PreviewRequest) error {
	if len(request.CompositionProposals) == 0 {
		return nil
	}
	if len(request.Proposals)+len(request.NativeProposals) != 0 || len(request.CompositionProposals) != len(request.PostingNumbers) || len(request.CompositionProposals) > orders.MaxPagePostings {
		return orders.ErrInvalid
	}
	seen := make(map[string]bool, len(request.CompositionProposals))
	for _, proposal := range request.CompositionProposals {
		if !proposal.ConfirmReplacement || !slices.Contains(request.PostingNumbers, proposal.PostingNumber) || seen[proposal.PostingNumber] {
			return orders.ErrInvalid
		}
		seen[proposal.PostingNumber] = true
	}
	for _, action := range request.Actions {
		if action != orders.ActionMirrorCompositionUpdate {
			return orders.ErrInvalid
		}
	}
	return nil
}

func (s *Service) planComposition(ctx context.Context, review orders.ReviewContext, posting orders.Posting, automatic bool, now time.Time) (orders.Decision, error) {
	decision := orders.Decision{Posting: posting, Commands: []orders.Command{}, Validation: sourceValidation(posting, review, now)}
	if automatic {
		return compositionBlocked(decision, "composition_manual_confirmation_required"), nil
	}
	correlation, err := postingCorrelation(posting, review.Correlations)
	if err != nil || correlation.CustomerOrderID == "" {
		return compositionBlocked(decision, "existing_customerorder_required"), nil
	}
	decision.Correlation = correlation
	warehouse, validation := postingWarehouse(posting, review)
	if validation.Code != "" {
		decision.Validation = append(decision.Validation, validation)
	}
	positions, validationList := ordermoney.ProjectLines(posting, review.ProductMappings)
	decision.Validation = append(decision.Validation, validationList...)
	write, validationList := mirrorWrite(ctx, posting, review, positions, s.options.CustomerReader)
	decision.Validation = append(decision.Validation, validationList...)
	if len(decision.Validation) > 0 {
		return decision, nil
	}
	command, err := mirrorCommand(review, decision, warehouse, write, false, now)
	if err != nil {
		return decision, err
	}
	target, err := s.readTarget(ctx, command.Scope, command.Credentials.Moysklad, command.Correlation)
	if err != nil {
		return compositionBlocked(decision, "target_readback_unavailable"), nil
	}
	decision.Target = &target
	return finishComposition(review, decision, command, target), nil
}

func finishComposition(review orders.ReviewContext, decision orders.Decision, command orders.Command, target orders.TargetObservation) orders.Decision {
	baseline, found := review.PositionBaselines[command.PostingNumber]
	if !found || !sameNativeReferences(*command.CustomerOrder, target.Fields) {
		return compositionBlocked(decision, "composition_ownership_unverified")
	}
	command.ExpectedTargetRevision = target.Revision
	command.TargetPositions, command.TargetPositionsComplete = slices.Clone(target.Positions), target.Complete
	replacement, err := orders.PlanPositionReplacement(command, baseline, target.Fields.VATEnabled)
	if err != nil {
		return compositionBlocked(decision, "composition_frontier_or_values_unverified")
	}
	command.Action, command.PositionReplacement = orders.ActionMirrorCompositionUpdate, replacement
	command.CustomerOrder.OwnedFields = []string{"positions.quantity", "positions.reserve", "positions.price", "positions.vat"}
	clearCompositionPrivateProjection(&command)
	decision.Validation = append(decision.Validation, commandPolicyValidation(command, review.Config)...)
	if command.Validate() != nil {
		return compositionBlocked(decision, "invalid_composition_intent")
	}
	if len(decision.Validation) == 0 {
		decision.Ready = true
		decision.Commands = []orders.Command{command}
	}
	return decision
}

func compositionBlocked(decision orders.Decision, code string) orders.Decision {
	decision.Validation = append(decision.Validation, orders.Validation{Field: "positions", Code: code})
	return decision
}

func clearCompositionPrivateProjection(command *orders.Command) {
	command.CustomerOrder.PIIMappings = nil
	command.CustomerOrder.TransientAttributes = nil
	command.CustomerOrder.ShipmentAddress = orders.Field[string]{}
	command.CustomerOrder.ShipmentAddressFull = orders.Field[orders.ShipmentAddress]{}
	command.ProtectedCustomer, command.PII = nil, nil
}
