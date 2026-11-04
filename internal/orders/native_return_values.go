package orders

// A return carries the prices and tax treatment of its exact demand position,
// not the customer order's current commercial values.
func (movement NativeMovement) validateReturnValues() error {
	seen := make(map[string]bool, len(movement.Lines))
	for _, line := range movement.Lines {
		basis, found := nativeReturnPosition(movement.DemandBasis.Positions, line.BasisPositionID)
		if !found || seen[line.BasisPositionID] || basis.ItemKind != line.Mapping.MoyskladKind || basis.ItemID != line.Mapping.MoyskladID || !nonnegativeNotGreater(line.Quantity, basis.Quantity) || !sameObservedPrice(Present(line.Price), Present(basis.Price)) || !SameObservedDecimal(line.Discount, basis.Discount) || !SameObservedDecimal(line.VAT, basis.VAT) || !nativeBoolEqual(line.VATEnabled, basis.VATEnabled) {
			return ErrStale
		}
		seen[line.BasisPositionID] = true
	}
	return nil
}

func nativeReturnPosition(positions []NativeDocumentPosition, id string) (NativeDocumentPosition, bool) {
	var result NativeDocumentPosition
	found := false
	for _, position := range positions {
		if position.ID != id {
			continue
		}
		if found {
			return NativeDocumentPosition{}, false
		}
		result, found = position, true
	}
	return result, found
}
