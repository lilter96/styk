package orders

import "slices"

func replacementPreserved(positions []TargetPosition, baseline PositionBaseline) ([]string, error) {
	owned := make(map[string]bool, len(baseline.Bindings))
	for _, binding := range baseline.Bindings {
		owned[binding.Native.ID] = true
	}
	seen := make(map[string]bool, len(positions))
	var result []string
	for _, position := range positions {
		if seen[position.ID] {
			return nil, ErrBlocked
		}
		seen[position.ID] = true
		if !owned[position.ID] {
			if !safePreservedPosition(position) {
				return nil, ErrBlocked
			}
			result = append(result, position.ID)
		}
	}
	slices.Sort(result)
	return result, nil
}

func safePreservedPosition(p TargetPosition) bool {
	return validNativePosition(p) && p.CompositionSafe.IsKnown() && *p.CompositionSafe.Value && p.Shipped.IsKnown() && decimalZero(*p.Shipped.Value) && p.Reserve.IsKnown() && nonnegativeNotGreater(*p.Reserve.Value, p.Quantity) && p.Price.IsKnown() && p.Discount.IsKnown() && nonnegativeNotGreater(*p.Discount.Value, "100") && p.VAT.IsKnown() && p.VATEnabled.IsKnown()
}

func replacementPreservedObserved(command Command, target TargetObservation) bool {
	seen := make(map[string]bool, len(target.Positions))
	for _, row := range target.Positions {
		if !validNativePosition(row) || seen[row.ID] {
			return false
		}
		seen[row.ID] = true
	}
	for _, id := range command.PositionReplacement.Preserved {
		before, found := nativePositionByID(command.TargetPositions, id)
		after, present := nativePositionByID(target.Positions, id)
		if !found || !present || !safePreservedPosition(before) || !safePreservedPosition(after) || !NativePositionEqual(before, after) {
			return false
		}
	}
	return true
}

func replacementNativePositionIndex(command Command, source PositionWrite, positions []TargetPosition) int {
	for _, retained := range command.PositionReplacement.Retained {
		if retained.SourceIdentity.Key() == source.Identity.Key() {
			return nativePositionIndexByID(positions, retained.NativePositionID)
		}
	}
	index := -1
	for i, position := range positions {
		if slices.Contains(command.PositionReplacement.Preserved, position.ID) || position.MoyskladKind != source.Mapping.MoyskladKind || position.MoyskladID != source.Mapping.MoyskladID {
			continue
		}
		if index >= 0 {
			return -1
		}
		index = i
	}
	return index
}

func nativePositionIndexByID(positions []TargetPosition, id string) int {
	result := -1
	for i, position := range positions {
		if position.ID == id {
			if result >= 0 {
				return -1
			}
			result = i
		}
	}
	return result
}
