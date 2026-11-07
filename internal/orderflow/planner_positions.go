package orderflow

import (
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

func validTargetPositions(positions []orders.TargetPosition) bool {
	seen := make(map[string]bool, len(positions))
	for _, position := range positions {
		if position.ID == "" || seen[position.ID] || position.Quantity.Validate() != nil || !validPositionFields(position) {
			return false
		}
		seen[position.ID] = true
	}
	return true
}

func validPositionFields(position orders.TargetPosition) bool {
	if position.Price.Validate() != nil || position.Reserve.Validate() != nil || position.VAT.Validate() != nil {
		return false
	}
	if position.Price.IsKnown() && position.Price.Value.Validate() != nil {
		return false
	}
	if position.Reserve.IsKnown() && position.Reserve.Value.Validate() != nil {
		return false
	}
	return !position.VAT.IsKnown() || position.VAT.Value.Validate() == nil
}

func positionsRepresented(wanted []orders.PositionWrite, actual []orders.TargetPosition) bool {
	if len(wanted) != len(actual) {
		return false
	}
	return wantedPositionsRepresented(wanted, actual)
}

func wantedPositionsRepresented(wanted []orders.PositionWrite, actual []orders.TargetPosition) bool {
	used := make(map[int]bool, len(actual))
	for _, position := range wanted {
		index := representedPositionIndex(position, wanted, actual)
		if index < 0 || used[index] || !positionValuesRepresented(position, actual[index]) {
			return false
		}
		used[index] = true
	}
	return true
}

func updatePositionsRepresented(command orders.Command, actual []orders.TargetPosition) bool {
	if command.CustomerOrder == nil {
		return false
	}
	if !command.TargetPositionsComplete {
		return positionsRepresented(command.CustomerOrder.Positions, actual)
	}
	if command.PositionBaseline != nil {
		return nativePositionsUnchanged(command.TargetPositions, actual) && orders.ScalarPositionFrontier(command, *command.PositionBaseline, actual)
	}
	return nativePositionsUnchanged(command.TargetPositions, actual) && wantedPositionsRepresented(command.CustomerOrder.Positions, actual)
}

func nativePositionsUnchanged(before, after []orders.TargetPosition) bool {
	return orders.NativePositionsEqual(before, after)
}

func representedPositionIndex(wanted orders.PositionWrite, all []orders.PositionWrite, actual []orders.TargetPosition) int {
	result := -1
	for index, candidate := range actual {
		if candidate.MoyskladKind != wanted.Mapping.MoyskladKind || candidate.MoyskladID != wanted.Mapping.MoyskladID {
			continue
		}
		if candidate.Identity.Validate() == nil && candidate.Identity.Key() != wanted.Identity.Key() {
			continue
		}
		if candidate.Identity.Validate() != nil && !uniqueWantedAssortment(wanted, all) {
			return -1
		}
		if result >= 0 {
			return -1
		}
		result = index
	}
	return result
}

func uniqueWantedAssortment(wanted orders.PositionWrite, all []orders.PositionWrite) bool {
	count := 0
	for _, position := range all {
		if position.Mapping.MoyskladKind == wanted.Mapping.MoyskladKind && position.Mapping.MoyskladID == wanted.Mapping.MoyskladID {
			count++
		}
	}
	return count == 1
}

func positionValuesRepresented(wanted orders.PositionWrite, actual orders.TargetPosition) bool {
	return actual.ID != "" && decimalEqual(wanted.Quantity, actual.Quantity) && decimalRepresented(wanted.Reserve, actual.Reserve) &&
		moneyRepresented(wanted.Price, actual.Price) && decimalRepresented(wanted.VAT, actual.VAT)
}

func decimalRepresented(wanted, actual orders.Field[orders.Decimal]) bool {
	return !wanted.IsKnown() || (actual.IsKnown() && decimalEqual(*wanted.Value, *actual.Value))
}

func moneyRepresented(wanted, actual orders.Field[orders.Money]) bool {
	return !wanted.IsKnown() || (actual.IsKnown() && wanted.Value.Currency == actual.Value.Currency && decimalEqual(wanted.Value.Amount, actual.Value.Amount))
}

func decimalEqual(left, right orders.Decimal) bool {
	if left.Validate() != nil || right.Validate() != nil {
		return false
	}
	return normalizedDecimal(left) == normalizedDecimal(right)
}

func normalizedDecimal(value orders.Decimal) string {
	result := string(value)
	if strings.Contains(result, ".") {
		result = strings.TrimRight(strings.TrimRight(result, "0"), ".")
	}
	if result == "-0" {
		return "0"
	}
	return result
}

func preparePositionPatch(command *orders.Command, baselines []orders.PositionBaseline) orders.Validation {
	if len(baselines) != 1 {
		return orders.Validation{Field: "positions", Code: "position_ownership_unverified"}
	}
	patch, err := orders.PlanPositionPatch(*command, baselines[0])
	if err != nil || patch == nil {
		if validation := ownedCompositionRequired(*command, baselines[0]); validation.Code != "" {
			return validation
		}
		return orders.Validation{Field: "positions", Code: "position_ownership_or_replacement_unverified"}
	}
	command.Action = orders.ActionMirrorPositionUpdate
	command.NativeStateBaseline = nil
	command.PositionPatch = patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	command.CustomerOrder.PIIMappings = nil
	command.CustomerOrder.TransientAttributes = nil
	command.CustomerOrder.ShipmentAddress = orders.Field[string]{}
	command.CustomerOrder.ShipmentAddressFull = orders.Field[orders.ShipmentAddress]{}
	command.ProtectedCustomer, command.PII = nil, nil
	return orders.Validation{}
}

func ownedCompositionRequired(command orders.Command, baseline orders.PositionBaseline) orders.Validation {
	if command.CustomerOrder == nil {
		return orders.Validation{}
	}
	wanted := make(map[string]bool, len(command.CustomerOrder.Positions))
	for _, position := range command.CustomerOrder.Positions {
		wanted[position.Identity.Key()] = true
	}
	missing := false
	for _, binding := range baseline.Bindings {
		missing = missing || !wanted[binding.Source.Identity.Key()]
	}
	if !missing {
		return orders.Validation{}
	}
	code := "composition_manual_confirmation_required"
	if command.RetainedSplitWorkID != "" || command.SplitAncestorSettlement != nil && command.SplitAncestorSettlement.Mode == orders.SplitAncestorReduce {
		code = "composition_split_replacement_required"
	}
	return orders.Validation{Field: "positions", Code: code}
}

// Metadata can be updated after a merchant recreates otherwise identical native
// rows. This exception never claims ownership of those UUIDs: every source
// inventory value must be unchanged, and readback freezes the entire actual
// frontier with PositionBaseline deliberately absent.
func metadataOnlyWithoutPositionAdoption(command orders.Command, baseline orders.PositionBaseline) bool {
	if command.Action != orders.ActionMirrorUpdate || command.CustomerOrder == nil || command.PositionBaseline != nil || !command.TargetPositionsComplete || baseline.Validate() != nil || baseline.Scope != command.Scope || baseline.Correlation != command.Correlation || !safeMetadataFields(command.CustomerOrder.OwnedFields) {
		return false
	}
	wanted := command.CustomerOrder.Positions
	if len(wanted) != len(baseline.Bindings) || !wantedPositionsRepresented(wanted, command.TargetPositions) {
		return false
	}
	previous := make(map[string]orders.PositionWrite, len(baseline.Bindings))
	for _, binding := range baseline.Bindings {
		previous[binding.Source.Identity.Key()] = binding.Source
	}
	for _, source := range wanted {
		prior, found := previous[source.Identity.Key()]
		if !found || !unchangedMetadataInventory(source, prior) {
			return false
		}
		delete(previous, source.Identity.Key())
	}
	return len(previous) == 0
}

func safeMetadataFields(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		switch field {
		case "name", "description", "moment", "delivery_at", "state", "attribute", "shipment_address":
			continue
		default:
			if !orders.ShipmentAddressTargetValid(field) {
				return false
			}
		}
	}
	return true
}

func unchangedMetadataInventory(source, prior orders.PositionWrite) bool {
	left, right := source.Mapping, prior.Mapping
	left.ConfirmedAt, right.ConfirmedAt = left.ConfirmedAt.UTC(), right.ConfirmedAt.UTC()
	return source.Identity == prior.Identity && left == right && orders.ExactDecimalEqual(source.Quantity, prior.Quantity) && orders.SameObservedDecimal(source.Reserve, prior.Reserve) && orders.SameObservedDecimal(source.VAT, prior.VAT) && unchangedMetadataPrice(source.Price, prior.Price)
}

func unchangedMetadataPrice(left, right orders.Field[orders.Money]) bool {
	if left.Validate() != nil || right.Validate() != nil {
		return false
	}
	if left.IsKnown() && right.IsKnown() {
		return left.Value.Currency == right.Value.Currency && orders.ExactDecimalEqual(left.Value.Amount, right.Value.Amount)
	}
	return sameField(left, right)
}
