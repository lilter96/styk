package orderinventory

import "github.com/lilter96/styk/internal/orders"

func nativeConsumptionKnownReturn(e orders.NativeReturnEvidence, o Observation, f orders.NativeConsumptionFacts) bool {
	for _, event := range f.Events {
		if event.Document == nil || event.Document.Correlation.Kind != "salesreturn" || event.Document.Correlation.DocumentID != e.ID {
			continue
		}
		point, found := o.NativeDocumentPoints[event.Work.ID]
		if !found || point.Observation == nil || point.Observation.Validate() != nil || !point.Observation.DemandID.IsKnown() || *point.Observation.DemandID.Value != e.DemandID || !point.Observation.Applicable.IsKnown() || *point.Observation.Applicable.Value != *e.Applicable.Value || len(point.Observation.Positions) != len(e.Positions) {
			return false
		}
		for _, position := range e.Positions {
			if !nativeConsumptionReturnPosition(position, point.Observation.Positions) {
				return false
			}
		}
		return true
	}
	return false // A manual/external return has not acquired STYK effect proof.
}

func nativeConsumptionReturnPosition(actual orders.NativeDocumentPosition, positions []orders.NativeDocumentPosition) bool {
	for _, expected := range positions {
		if expected.ID != actual.ID {
			continue
		}
		return expected.ItemKind == actual.ItemKind && expected.ItemID == actual.ItemID && orders.ExactDecimalEqual(expected.Quantity, actual.Quantity) && expected.Price.Currency == actual.Price.Currency && orders.ExactDecimalEqual(expected.Price.Amount, actual.Price.Amount) && orders.SameObservedDecimal(expected.Discount, actual.Discount) && orders.SameObservedDecimal(expected.VAT, actual.VAT) && expected.VATEnabled.IsKnown() && actual.VATEnabled.IsKnown() && *expected.VATEnabled.Value == *actual.VATEnabled.Value
	}
	return false
}
