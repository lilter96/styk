package ordermoney

import "github.com/lilter96/styk/internal/orders"

func (h *nativeConsumptionHistory) returnCapacity(c orders.Command, line orders.NativeMovementLine) orders.Validation {
	m := c.NativeMovement
	basis, found := h.works[m.DemandWorkID]
	if !found || basis.Document == nil || m.DemandBasis == nil || basis.Work.Command.Action != orders.ActionDemandCreate || !basis.Work.Command.NativeMovement.Applicable.IsKnown() || !*basis.Work.Command.NativeMovement.Applicable.Value || m.DemandBasis.Correlation != basis.Document.Correlation || !m.DemandBasis.ReturnsComplete || !orders.NativeDocumentMatches(basis.Work.Command, *m.DemandBasis) {
		return consumptionIssue("native_consumption_return_basis_unproven")
	}
	position, found := nativeConsumptionBasisPosition(basis.Document.Positions, line.BasisPositionID)
	if !found || position.ItemKind != line.Mapping.MoyskladKind || position.ItemID != line.Mapping.MoyskladID {
		return consumptionIssue("native_consumption_return_position_unproven")
	}
	consumed := h.returnedQuantities(m.DemandWorkID, line.BasisPositionID)
	for _, returned := range m.DemandBasis.Returns {
		if returned.Validate(basis.Document.Correlation.DocumentID, position.Price.Currency) != nil {
			return consumptionIssue("native_consumption_external_return_unproven")
		}
		known, matches := h.knownReturn(returned)
		if known && !matches {
			return consumptionIssue("native_consumption_return_changed")
		}
		if known || !*returned.Applicable.Value {
			continue
		}
		for _, row := range returned.Positions {
			if row.ItemKind == position.ItemKind && row.ItemID == position.ItemID {
				consumed = append(consumed, row.Quantity)
			}
		}
	}
	consumed = append(consumed, line.Quantity)
	if _, err := RemainingQuantity(position.Quantity, consumed...); err != nil {
		return consumptionIssue("native_consumption_return_exceeds_demand")
	}
	return orders.Validation{}
}

func (h *nativeConsumptionHistory) returnedQuantities(demandWork, position string) []orders.Decimal {
	quantities := make([]orders.Decimal, 0)
	for _, event := range h.works {
		m := event.Work.Command.NativeMovement
		if event.Work.State != orders.StateReconciled || event.Work.Command.Action != orders.ActionSalesReturnCreate || m.DemandWorkID != demandWork || !m.Applicable.IsKnown() || !*m.Applicable.Value {
			continue
		}
		for _, line := range m.Lines {
			if line.BasisPositionID == position {
				quantities = append(quantities, line.Quantity)
			}
		}
	}
	return quantities
}

func nativeConsumptionBasisPosition(positions []orders.NativeDocumentPosition, id string) (orders.NativeDocumentPosition, bool) {
	var result orders.NativeDocumentPosition
	found := false
	for _, position := range positions {
		if position.ID == id {
			if found {
				return result, false
			}
			result, found = position, true
		}
	}
	return result, found
}

func (h *nativeConsumptionHistory) knownReturn(actual orders.NativeReturnEvidence) (bool, bool) {
	for _, event := range h.works {
		prior := event.Document
		if prior == nil || prior.Correlation.Kind != "salesreturn" || prior.Correlation.DocumentID != actual.ID {
			continue
		}
		if !prior.Applicable.IsKnown() || *prior.Applicable.Value != *actual.Applicable.Value || len(prior.Positions) != len(actual.Positions) {
			return true, false
		}
		for _, position := range actual.Positions {
			before, found := nativeConsumptionBasisPosition(prior.Positions, position.ID)
			if !found || !nativeConsumptionSamePosition(before, position) {
				return true, false
			}
		}
		return true, true
	}
	return false, false
}

func nativeConsumptionSamePosition(before, after orders.NativeDocumentPosition) bool {
	return before.ItemKind == after.ItemKind && before.ItemID == after.ItemID && orders.ExactDecimalEqual(before.Quantity, after.Quantity) && before.Price.Currency == after.Price.Currency && orders.ExactDecimalEqual(before.Price.Amount, after.Price.Amount) && orders.SameObservedDecimal(before.Discount, after.Discount) && orders.SameObservedDecimal(before.VAT, after.VAT) && before.VATEnabled.IsKnown() && after.VATEnabled.IsKnown() && *before.VATEnabled.Value == *after.VATEnabled.Value
}
