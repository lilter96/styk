package ordermoney

import "github.com/lilter96/styk/internal/orders"

// NativeParentEffectObserved requires the parent shipment counter to reflect
// this exact posted demand. A posted document alone cannot refresh that fact.
// It does not assign reserve, shipped quantity or physical stock.
func NativeParentEffectObserved(command orders.Command, target orders.TargetObservation) bool {
	movement := command.NativeMovement
	if movement == nil || !movement.Applicable.IsKnown() {
		return false
	}
	if command.Action != orders.ActionDemandCreate || !*movement.Applicable.Value {
		return true
	}
	if movement.ParentEffect == orders.NativeParentUnpostedDocument {
		return orders.NativeParentReadback(command, target) && orders.NativeUnpostedParentEffect(command, target)
	}
	for _, line := range movement.Lines {
		before, beforeFound := nativeParentCounter(command.TargetPositions, line.OrderPositionID)
		after, afterFound := nativeParentCounter(target.Positions, line.OrderPositionID)
		if !beforeFound || !afterFound {
			return false
		}
		difference, err := RemainingQuantity(after, before)
		if err != nil || !orders.ExactDecimalEqual(difference, line.Quantity) {
			return false
		}
	}
	return true
}

func nativeParentCounter(positions []orders.TargetPosition, id string) (orders.Decimal, bool) {
	result, found := orders.Decimal(""), false
	for _, position := range positions {
		if position.ID != id {
			continue
		}
		if found || !position.Shipped.IsKnown() {
			return "", false
		}
		result, found = *position.Shipped.Value, true
	}
	return result, found
}
