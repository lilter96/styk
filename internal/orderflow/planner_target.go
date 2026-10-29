package orderflow

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Service) finishMirrorPlan(ctx context.Context, review orders.ReviewContext, decision orders.Decision, command orders.Command, automatic bool, now time.Time) (orders.Decision, error) {
	if command.Action == orders.ActionMirrorUpdate {
		target, err := s.readTarget(ctx, command.Scope, command.Credentials.Moysklad, command.Correlation)
		if err != nil {
			decision.Validation = append(decision.Validation, orders.Validation{Field: "target", Code: "target_readback_unavailable"})
			return decision, nil
		}
		decision.Target = &target
		if validation := prepareMirrorUpdate(&command, target); validation.Code != "" {
			decision.Validation = append(decision.Validation, validation)
			return decision, nil
		}
		if targetRepresents(command, target) {
			decision.Ready = true
			return decision, nil
		}
	}
	decision.Validation = append(decision.Validation, commandPolicyValidation(command, review.Config)...)
	if command.Validate() != nil {
		decision.Validation = append(decision.Validation, orders.Validation{Field: "command", Code: "invalid_mirror_intent"})
	}
	if automatic && (review.Config.Automation == nil || !review.Config.Automation.Allows(command, now)) {
		decision.Validation = append(decision.Validation, orders.Validation{Field: "automation", Code: "automation_grant_does_not_allow_intent"})
	}
	if len(decision.Validation) == 0 {
		decision.Commands, decision.Ready = []orders.Command{command}, true
	}
	return decision, nil
}

func (s *Service) readTarget(ctx context.Context, scope orders.Scope, revision int64, correlation orders.Correlation) (orders.TargetObservation, error) {
	client, err := resolveOrderClient(ctx, s.resolver, scope, "moysklad", revision)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return orders.TargetObservation{}, orders.ErrBlocked
	}
	target, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	if !validTarget(correlation, target) {
		return orders.TargetObservation{}, orders.ErrStale
	}
	return target, nil
}

func prepareMirrorUpdate(command *orders.Command, target orders.TargetObservation) orders.Validation {
	command.ExpectedTargetRevision = target.Revision
	command.TargetPositions, command.TargetPositionsComplete = slices.Clone(target.Positions), target.Complete
	wanted := command.CustomerOrder
	if !sameNativeReferences(*wanted, target.Fields) {
		return orders.Validation{Field: "target", Code: "target_native_references_conflict"}
	}
	wanted.OwnedFields = changedScalarFields(*wanted, target.Fields)
	if !updatePositionsRepresented(*command, target.Positions) {
		return orders.Validation{Field: "positions", Code: "position_replacement_unverified"}
	}
	return orders.Validation{}
}

func changedScalarFields(wanted, actual orders.CustomerOrderWrite) []string {
	fields := make([]string, 0, 9)
	fields = appendChanged(fields, "name", !fieldRepresented(wanted.Name, actual.Name))
	fields = appendChanged(fields, "description", !fieldRepresented(wanted.Description, actual.Description))
	fields = appendChanged(fields, "moment", !timeRepresented(wanted.Moment, actual.Moment))
	fields = appendChanged(fields, "delivery_at", !timeRepresented(wanted.DeliveryAt, actual.DeliveryAt))
	fields = appendChanged(fields, "applicable", !fieldRepresented(wanted.Applicable, actual.Applicable))
	fields = appendChanged(fields, "vat_enabled", !fieldRepresented(wanted.VATEnabled, actual.VATEnabled))
	fields = appendChanged(fields, "vat_included", !fieldRepresented(wanted.VATIncluded, actual.VATIncluded))
	fields = appendChanged(fields, "state", wanted.StateID != actual.StateID)
	return appendChanged(fields, "attribute", !attributesRepresented(wanted.Attributes, actual.Attributes))
}

func appendChanged(fields []string, field string, changed bool) []string {
	if changed {
		return append(fields, field)
	}
	return fields
}

func commandPolicyValidation(command orders.Command, config orders.Config) []orders.Validation {
	result := make([]orders.Validation, 0)
	for _, field := range command.Fields() {
		if field != "state" && !mappedTarget(config, field) {
			result = append(result, orders.Validation{Field: field, Code: "field_not_authorized"})
		}
	}
	return result
}

func validTarget(correlation orders.Correlation, target orders.TargetObservation) bool {
	actual := target.Correlation
	if !target.Complete || target.Scope != correlation.Scope || target.Revision == "" || target.ObservedAt.IsZero() || actual.Validate() != nil {
		return false
	}
	if !validTargetFields(target.Fields) || !validTargetPositions(target.Positions) {
		return false
	}
	if actual.Scope != correlation.Scope || actual.PostingNumber != correlation.PostingNumber || actual.SyncID != correlation.SyncID || actual.Marker != correlation.Marker || actual.CustomerOrderID == "" {
		return false
	}
	return correlation.CustomerOrderID == "" || actual.CustomerOrderID == correlation.CustomerOrderID
}

func validTargetFields(fields orders.CustomerOrderWrite) bool {
	if fields.Name.Validate() != nil || fields.Description.Validate() != nil || fields.Moment.Validate() != nil || fields.DeliveryAt.Validate() != nil {
		return false
	}
	if fields.Applicable.Validate() != nil || fields.VATEnabled.Validate() != nil || fields.VATIncluded.Validate() != nil || fields.RateValue.Validate() != nil {
		return false
	}
	seen := make(map[string]bool, len(fields.Attributes))
	for _, attribute := range fields.Attributes {
		if attribute.AttributeID == "" || seen[attribute.AttributeID] || attribute.Value.Validate() != nil {
			return false
		}
		seen[attribute.AttributeID] = true
	}
	return true
}

func targetRepresents(command orders.Command, target orders.TargetObservation) bool {
	if command.CustomerOrder == nil || !validTarget(command.Correlation, target) {
		return false
	}
	wanted := *command.CustomerOrder
	if !sameNativeReferences(wanted, target.Fields) || len(changedScalarFields(wanted, target.Fields)) > 0 {
		return false
	}
	if command.Action == orders.ActionMirrorUpdate {
		return updatePositionsRepresented(command, target.Positions)
	}
	return positionsRepresented(wanted.Positions, target.Positions)
}

func sameNativeReferences(wanted, actual orders.CustomerOrderWrite) bool {
	return wanted.OrganizationID == actual.OrganizationID && wanted.AgentID == actual.AgentID &&
		wanted.StoreID == actual.StoreID && wanted.SalesChannelID == actual.SalesChannelID &&
		(wanted.CurrencyID == "" || wanted.CurrencyID == actual.CurrencyID)
}

func fieldRepresented[T comparable](wanted, actual orders.Field[T]) bool {
	return !wanted.IsKnown() || (actual.IsKnown() && *wanted.Value == *actual.Value)
}

func sameField[T comparable](left, right orders.Field[T]) bool {
	if left.IsMissing() || right.IsMissing() {
		return left.IsMissing() && right.IsMissing()
	}
	if left.Presence != right.Presence {
		return false
	}
	if left.Value == nil || right.Value == nil {
		return left.Value == nil && right.Value == nil
	}
	return *left.Value == *right.Value
}

func timeRepresented(wanted, actual orders.Field[time.Time]) bool {
	return !wanted.IsKnown() || (actual.IsKnown() && wanted.Value.Equal(*actual.Value))
}

func attributesRepresented(wanted, actual []orders.AttributeWrite) bool {
	for _, attribute := range wanted {
		found := false
		for _, candidate := range actual {
			if candidate.AttributeID == attribute.AttributeID && fieldRepresented(attribute.Value, candidate.Value) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
