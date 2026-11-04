package orders

import "time"

// NativeDocumentMatches checks actual provider facts. HTTP acceptance alone
// never establishes a posted movement, a shipment, a refund or a stock return.
func NativeDocumentMatches(command Command, actual NativeDocumentObservation) bool {
	wanted := command.NativeMovement
	if wanted == nil || actual.Validate() != nil || actual.Scope != command.Scope || actual.Correlation.Kind != wanted.Correlation.Kind || actual.Correlation.SyncID != wanted.Correlation.SyncID || actual.Correlation.Marker != wanted.Correlation.Marker {
		return false
	}
	if wanted.Correlation.DocumentID != "" && wanted.Correlation.DocumentID != actual.Correlation.DocumentID {
		return false
	}
	if wanted.OrganizationID != actual.OrganizationID || wanted.AgentID != actual.AgentID || wanted.StoreID != actual.StoreID || wanted.CurrencyID != actual.CurrencyID || !nativeBoolEqual(wanted.Applicable, actual.Applicable) || !nativeBoolEqual(wanted.VATEnabled, actual.VATEnabled) || !nativeBoolEqual(wanted.VATIncluded, actual.VATIncluded) || !desiredDecimal(wanted.RateValue, actual.RateValue) {
		return false
	}
	if !actual.Moment.IsKnown() || !wanted.Moment.UTC().Truncate(time.Minute).Equal(actual.Moment.Value.UTC().Truncate(time.Minute)) {
		return false
	}
	if command.Action == ActionDemandCreate && (!actual.CustomerOrderID.IsKnown() || *actual.CustomerOrderID.Value != command.Correlation.CustomerOrderID) {
		return false
	}
	if command.Action == ActionSalesReturnCreate && (wanted.DemandBasis == nil || !actual.DemandID.IsKnown() || *actual.DemandID.Value != wanted.DemandBasis.Correlation.DocumentID) {
		return false
	}
	return nativeDocumentLinesMatch(wanted.Lines, actual.Positions)
}

func nativeBoolEqual(a, b Field[bool]) bool {
	return knownNativeBoolean(a) && knownNativeBoolean(b) && *a.Value == *b.Value
}

func nativeDocumentLinesMatch(wanted []NativeMovementLine, actual []NativeDocumentPosition) bool {
	if len(wanted) != len(actual) {
		return false
	}
	used := make(map[string]bool, len(wanted))
	for _, line := range wanted {
		index := -1
		for i, position := range actual {
			if position.ItemKind != line.Mapping.MoyskladKind || position.ItemID != line.Mapping.MoyskladID {
				continue
			}
			if index >= 0 {
				return false
			}
			index = i
		}
		if index < 0 || used[actual[index].ID] || !nativeDocumentLineEqual(line, actual[index]) {
			return false
		}
		used[actual[index].ID] = true
	}
	return true
}

func nativeDocumentLineEqual(wanted NativeMovementLine, actual NativeDocumentPosition) bool {
	return ExactDecimalEqual(wanted.Quantity, actual.Quantity) && wanted.Price.Currency == actual.Price.Currency && ExactDecimalEqual(wanted.Price.Amount, actual.Price.Amount) && desiredDecimal(wanted.Discount, actual.Discount) && desiredDecimal(wanted.VAT, actual.VAT) && nativeBoolEqual(wanted.VATEnabled, actual.VATEnabled)
}

// NativeParentReadback preserves all foreign lines and only permits native
// posted-document reserve/shipped effects on explicitly selected owned lines.
func NativeParentReadback(command Command, target TargetObservation) bool {
	movement := command.NativeMovement
	if movement == nil || !target.Complete || target.Scope != command.Scope || !baselineCorrelationMatches(command.Correlation, target.Correlation) || target.Correlation.CustomerOrderID != command.Correlation.CustomerOrderID || target.ObservedAt.IsZero() || len(target.Positions) != len(command.TargetPositions) {
		return false
	}
	if target.Fields.OrganizationID != movement.OrganizationID || target.Fields.AgentID != movement.AgentID || target.Fields.StoreID != movement.StoreID || target.Fields.CurrencyID != movement.CurrencyID {
		return false
	}
	if !movement.Applicable.IsKnown() {
		return false
	}
	if !*movement.Applicable.Value {
		return NativePositionsEqual(command.TargetPositions, target.Positions)
	}
	selected := make(map[string]bool, len(movement.Lines))
	for _, line := range movement.Lines {
		selected[line.OrderPositionID] = true
	}
	after, unique := nativeParentPositionIndex(target.Positions)
	if !unique {
		return false
	}
	for _, before := range command.TargetPositions {
		current, found := after[before.ID]
		if !found {
			return false
		}
		if !selected[before.ID] {
			if !NativePositionEqual(before, current) {
				return false
			}
			continue
		}
		if !nativeParentPositionEffect(command.Action, before, current) {
			return false
		}
	}
	return true
}

func nativeParentPositionEffect(action Action, before, after TargetPosition) bool {
	if !before.Reserve.IsKnown() || !after.Reserve.IsKnown() || !before.Shipped.IsKnown() || !after.Shipped.IsKnown() {
		return false
	}
	if action == ActionDemandCreate && (!nonnegativeNotGreater(*after.Reserve.Value, *before.Reserve.Value) || !nonnegativeNotGreater(*before.Shipped.Value, *after.Shipped.Value) || !nonnegativeNotGreater(*after.Shipped.Value, after.Quantity)) {
		return false
	}
	// A return need not alter the customer order's shipped/reserve counters.
	// Learn the provider counters rather than writing or inferring them.
	if action == ActionSalesReturnCreate && (!SameObservedDecimal(before.Reserve, after.Reserve) || !SameObservedDecimal(before.Shipped, after.Shipped)) {
		return false
	}
	after.Reserve, after.Shipped = before.Reserve, before.Shipped
	return NativePositionEqual(before, after)
}

// NativeParentFacts deliberately excludes customer names, addresses, free text
// and unrelated attributes from movement previews and immutable evidence.
func NativeParentFacts(target TargetObservation) TargetObservation {
	fields := target.Fields
	target.Fields = CustomerOrderWrite{OrganizationID: fields.OrganizationID, AgentID: fields.AgentID, StoreID: fields.StoreID, CurrencyID: fields.CurrencyID, RateValue: fields.RateValue, VATEnabled: fields.VATEnabled, VATIncluded: fields.VATIncluded, Applicable: fields.Applicable, StateID: fields.StateID}
	return target
}

func nativeParentPositionIndex(positions []TargetPosition) (map[string]TargetPosition, bool) {
	result := make(map[string]TargetPosition, len(positions))
	for _, position := range positions {
		if position.ID == "" {
			return nil, false
		}
		if _, exists := result[position.ID]; exists {
			return nil, false
		}
		result[position.ID] = position
	}
	return result, true
}
