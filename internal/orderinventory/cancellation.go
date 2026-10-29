package orderinventory

import "github.com/lilter96/styk/internal/orders"

// An exact, fresh, complete zero-reserve readback of a cancelled order needs no
// additional deduction. This never releases or rewrites the source reserve and
// says nothing about physical returns or reserves belonging to other orders.
func cancelledZeroReadback(input Input, observation Observation) bool {
	target := observation.Target
	if !knownStatus(observation.Posting, "cancelled") || target == nil || target.Fields.StoreID != input.Pool.WarehouseID || !freshCancellation(input, observation) {
		return false
	}
	seen := make(map[string]bool, len(target.Positions))
	for _, position := range target.Positions {
		if position.MoyskladKind == "" || position.MoyskladID == "" {
			return false
		}
		if position.MoyskladKind != input.Pool.ItemKind || position.MoyskladID != input.Pool.ItemID {
			continue
		}
		if !explicitZeroReserve(position) || position.ID == "" || seen[position.ID] {
			return false
		}
		seen[position.ID] = true
	}
	return len(seen) > 0
}

func freshCancellation(input Input, observation Observation) bool {
	sourceAt := observation.Posting.Evidence.ObservedAt
	if checked := observation.Posting.Evidence.CheckedAt; checked.After(sourceAt) {
		sourceAt = checked
	}
	if sourceAt.IsZero() || sourceAt.After(input.Now) || input.MaxAge <= 0 || input.Now.Sub(sourceAt) >= input.MaxAge {
		return false
	}
	targetAt := observation.Target.ObservedAt
	return !targetAt.IsZero() && !targetAt.After(input.Now) && input.Now.Sub(targetAt) < input.MaxAge
}

func explicitZeroReserve(position orders.TargetPosition) bool {
	if position.Reserve.Validate() != nil || !position.Reserve.IsKnown() || position.Reserve.Value == nil {
		return false
	}
	_, quantityOK := whole(string(position.Quantity))
	reserve, reserveOK := whole(string(*position.Reserve.Value))
	return quantityOK && reserveOK && reserve == 0
}
