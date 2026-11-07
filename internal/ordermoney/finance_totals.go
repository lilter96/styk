package ordermoney

import (
	"sort"

	"github.com/lilter96/styk/internal/orders"
)

func financialCategoryTotals(components []orders.FinancialComponent) ([]orders.FinancialCategoryTotal, error) {
	groups := make(map[string]orders.FinancialCategoryTotal)
	for _, component := range components {
		key := component.Category + ":" + component.Side + ":" + component.Role
		total, exists := groups[key]
		if !exists {
			total = orders.FinancialCategoryTotal{Category: component.Category, Side: component.Side, Role: component.Role, Currency: component.Currency, Sum: "0"}
		}
		var err error
		total, err = appendFinancialCategory(total, component)
		if err != nil {
			return nil, err
		}
		groups[key] = total
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]orders.FinancialCategoryTotal, 0, len(keys))
	for _, key := range keys {
		result = append(result, groups[key])
	}
	return result, nil
}

func appendFinancialCategory(total orders.FinancialCategoryTotal, component orders.FinancialComponent) (orders.FinancialCategoryTotal, error) {
	if component.Value.IsMissing() {
		total.Missing++
		return total, nil
	}
	if component.Value.Presence == orders.PresenceNull {
		total.Null++
		return total, nil
	}
	if !component.Value.IsKnown() {
		return total, orders.ErrInvalid
	}
	left, err := parseDecimal(total.Sum)
	if err != nil {
		return total, err
	}
	right, err := parseDecimal(*component.Value.Value)
	if err != nil {
		return total, err
	}
	total.Sum, err = left.plus(right).decimal()
	if err != nil {
		return total, err
	}
	total.Known++
	switch right.coefficient.Sign() {
	case -1:
		total.Negative++
	case 0:
		total.Zero++
	case 1:
		total.Positive++
	}
	return total, nil
}
