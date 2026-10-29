package orderflow

import (
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

func positionsRepresented(wanted []orders.PositionWrite, actual []orders.TargetPosition) bool {
	if len(wanted) != len(actual) {
		return false
	}
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
