package ordermoney

import "github.com/lilter96/styk/internal/orders"

// Revalidate the frozen return frontier at the payload boundary, not only in
// the preview planner. Draft returns do not consume physical return quantity.
func nativeReturnAmounts(movement *orders.NativeMovement) orders.Validation {
	basis := movement.DemandBasis
	if basis == nil {
		return orders.Validation{}
	}
	if basis.Validate() != nil || !basis.ReturnsComplete {
		return nativeAmountIssue("native_return_basis_unknown")
	}
	seen := make(map[string]bool, len(movement.Lines))
	for _, line := range movement.Lines {
		quantity, found := nativeReturnBasisQuantity(basis.Positions, line.BasisPositionID)
		if !found || seen[line.BasisPositionID] {
			return nativeAmountIssue("native_return_basis_unknown")
		}
		seen[line.BasisPositionID] = true
		consumed := nativeReturnConsumed(*basis, line)
		if _, err := RemainingQuantity(quantity, consumed...); err != nil {
			return nativeAmountIssue("native_return_quantity_exceeded")
		}
	}
	return orders.Validation{}
}

func nativeReturnConsumed(basis orders.NativeDocumentObservation, line orders.NativeMovementLine) []orders.Decimal {
	consumed := []orders.Decimal{line.Quantity}
	for _, returned := range basis.Returns {
		if !*returned.Applicable.Value {
			continue
		}
		for _, row := range returned.Positions {
			if row.ItemKind == line.Mapping.MoyskladKind && row.ItemID == line.Mapping.MoyskladID {
				consumed = append(consumed, row.Quantity)
			}
		}
	}
	return consumed
}

func nativeReturnBasisQuantity(positions []orders.NativeDocumentPosition, id string) (orders.Decimal, bool) {
	for _, position := range positions {
		if position.ID == id {
			return position.Quantity, true
		}
	}
	return "", false
}
