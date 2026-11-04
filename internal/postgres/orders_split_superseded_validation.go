package postgres

import (
	"github.com/lilter96/styk/internal/orders"
)

func validateSupersededSplitCommand(command orders.Command, current orders.ReviewContext) error {
	facts, found := current.SplitSupersededReserves[command.PostingNumber]
	if command.SupersededSplitWorkID == "" {
		if command.Action.IsMirror() && found {
			return orders.ErrStale
		}
		return nil
	}
	if !found || facts.Split.ID != command.SupersededSplitWorkID || command.CustomerOrder == nil {
		return orders.ErrStale
	}
	facts.Target = orders.TargetObservation{Scope: command.Scope, Correlation: command.Correlation,
		Revision: command.ExpectedTargetRevision, ObservedAt: command.CreatedAt, Complete: command.TargetPositionsComplete,
		Fields: *command.CustomerOrder, Positions: command.TargetPositions}
	return validateSupersededSplitPlan(command, facts)
}

func validateSupersededSplitPlan(command orders.Command, facts orders.SplitSupersededReserveFacts) error {
	permission, err := orders.NewSupersededSplitReserve(facts)
	if err != nil {
		return orders.ErrBlocked
	}
	plan, err := orders.PlanSupersededSplitReserve(command, permission)
	if err != nil || plan.PositionPatch == nil {
		return orders.ErrStale
	}
	expected := command
	expected.PositionPatch, expected.CustomerOrder = plan.PositionPatch, &plan.CustomerOrder
	have, err := orders.CommandFingerprint(command)
	if err != nil {
		return err
	}
	want, err := orders.CommandFingerprint(expected)
	if err != nil || have != want {
		return orders.ErrStale
	}
	return nil
}
