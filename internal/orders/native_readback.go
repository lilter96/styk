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
