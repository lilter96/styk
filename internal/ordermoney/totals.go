package ordermoney

import (
	"github.com/lilter96/styk/internal/orders"
)

func validateTotals(financial orders.FinancialFacts, positions []orders.PositionWrite) orders.Validation {
	total, currency, err := sumPositions(positions)
	if err != nil {
		return orders.Validation{Field: "lines[].price", Code: "currency_mismatch"}
	}
	if financial.Currency.Validate() != nil {
		return orders.Validation{Field: "financial.currency", Code: "invalid_currency"}
	}
	if financial.Currency.IsKnown() && *financial.Currency.Value != currency {
		return orders.Validation{Field: "financial.currency", Code: "currency_mismatch"}
	}
	if _, err := total.rounded(2).decimal(); err != nil {
		return orders.Validation{Field: "financial.order_total", Code: "total_out_of_range"}
	}
	return validateReportedTotal(financial.OrderTotal, total, currency)
}

// A computed check is only a diagnostic against an explicit reported total.
// It is not MoySklad's provider-computed sum and is never written as that field.
func validateReportedTotal(reported orders.Field[orders.Money], calculated exactDecimal, currency string) orders.Validation {
	if reported.Validate() != nil {
		return orders.Validation{Field: "financial.order_total", Code: "invalid_order_total"}
	}
	if !reported.IsKnown() {
		return orders.Validation{}
	}
	if reported.Value.Currency != currency {
		return orders.Validation{Field: "financial.order_total", Code: "currency_mismatch"}
	}
	amount, scale, err := monetaryDecimal(*reported.Value)
	if err != nil {
		return orders.Validation{Field: "financial.order_total", Code: "invalid_order_total"}
	}
	if !calculated.rounded(scale).equal(amount) {
		return orders.Validation{Field: "financial.order_total", Code: "order_total_mismatch"}
	}
	return orders.Validation{}
}

func sumPositions(positions []orders.PositionWrite) (exactDecimal, string, error) {
	total, err := parseDecimal("0")
	if err != nil {
		return exactDecimal{}, "", err
	}
	currency := positions[0].Price.Value.Currency
	for _, position := range positions {
		if position.Price.Value.Currency != currency {
			return exactDecimal{}, "", orders.ErrConflict
		}
		quantity, err := parseDecimal(position.Quantity)
		if err != nil {
			return exactDecimal{}, "", err
		}
		price, _, err := monetaryDecimal(*position.Price.Value)
		if err != nil {
			return exactDecimal{}, "", err
		}
		total = total.plus(quantity.times(price))
	}
	return total, currency, nil
}
