package orderflow

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Service) planSupersededSplitReserve(ctx context.Context, review orders.ReviewContext, decision orders.Decision, automatic bool, now time.Time) (orders.Decision, error) {
	facts, found := review.SplitSupersededReserves[decision.Posting.PostingNumber]
	if !found || !supersededReserveConfigured(review.Config) || decision.Correlation != facts.OriginalCorrelation {
		return blockedSupersededDecision(decision), nil
	}
	warehouse, invalid := postingWarehouse(decision.Posting, review)
	if invalid.Code != "" {
		decision.Validation = append(decision.Validation, invalid)
		return decision, nil
	}
	target, err := s.readTarget(ctx, decision.Posting.Scope, review.Credentials.Moysklad, decision.Correlation)
	if err != nil {
		return blockedSupersededDecision(decision), nil
	}
	facts.Target, facts.Parent = target, decision.Posting
	permission, err := orders.NewSupersededSplitReserve(facts)
	if err != nil {
		return blockedSupersededDecision(decision), nil
	}
	write := supersededReserveWrite(review.Config, target, facts.Baseline)
	command, err := mirrorCommand(review, decision, warehouse, write, automatic, now)
	if err != nil {
		return orders.Decision{}, err
	}
	command.SupersededSplitWorkID = facts.Split.ID
	command.ExpectedTargetRevision = target.Revision
	command.TargetPositions, command.TargetPositionsComplete = slices.Clone(target.Positions), target.Complete
	return finishSupersededReservePlan(review, decision, command, target, permission, automatic, now), nil
}

func finishSupersededReservePlan(review orders.ReviewContext, decision orders.Decision, command orders.Command, target orders.TargetObservation, permission orders.SplitSupersededReservePermission, automatic bool, now time.Time) orders.Decision {
	plan, err := orders.PlanSupersededSplitReserve(command, permission)
	if err != nil {
		return blockedSupersededDecision(decision)
	}
	decision.Target = &target
	if plan.PositionPatch == nil {
		decision.Ready = true
		return decision
	}
	command.Action, command.PositionPatch, command.CustomerOrder = orders.ActionMirrorPositionUpdate, plan.PositionPatch, &plan.CustomerOrder
	decision.Validation = append(decision.Validation, commandPolicyValidation(command, review.Config)...)
	if command.Validate() != nil || automatic && (review.Config.Automation == nil || !review.Config.Automation.Allows(command, now)) {
		return blockedSupersededDecision(decision)
	}
	if len(decision.Validation) == 0 {
		decision.Ready, decision.Commands = true, []orders.Command{command}
	}
	return decision
}

func supersededReserveWrite(config orders.Config, target orders.TargetObservation, baseline orders.PositionBaseline) orders.CustomerOrderWrite {
	write := orders.CustomerOrderWrite{OrganizationID: config.OrganizationID, AgentID: config.AgentID, StoreID: config.StoreID,
		StateID: target.Fields.StateID, SalesChannelID: config.SalesChannelID, CurrencyID: config.CurrencyID,
		Applicable: orders.Present(true), Positions: make([]orders.PositionWrite, 0, len(baseline.Bindings))}
	for _, binding := range baseline.Bindings {
		write.Positions = append(write.Positions, binding.Source)
	}
	return write
}

func supersededReserveConfigured(config orders.Config) bool {
	return slices.ContainsFunc(config.FieldMappings, func(mapping orders.FieldMapping) bool {
		return mapping.Source == orders.ReserveOpenQuantitySource && mapping.Target == "positions.reserve"
	})
}

func blockedSupersededDecision(decision orders.Decision) orders.Decision {
	decision.Ready, decision.Commands = false, nil
	decision.Validation = append(decision.Validation, orders.Validation{Field: "posting.lineage", Code: "superseded_split_reserve_proof_required"})
	return decision
}
