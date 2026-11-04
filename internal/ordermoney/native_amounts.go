package ordermoney

import "github.com/lilter96/styk/internal/orders"

// NativeDocumentAmounts rejects a rounding-dependent plan before dispatch.
// Ozon commercial-total rounding is not a documented MoySklad movement rule.
func NativeDocumentAmounts(movement *orders.NativeMovement) orders.Validation {
	if movement == nil || len(movement.Lines) == 0 {
		return nativeAmountIssue("native_sum_unknown")
	}
	if issue := nativeReturnAmounts(movement); issue.Code != "" {
		return issue
	}
	currency := movement.Lines[0].Price.Currency
	for _, line := range movement.Lines {
		if line.Price.Currency != currency {
			return nativeAmountIssue("currency_mismatch")
		}
		if !line.Discount.IsKnown() || !orders.ExactDecimalEqual(*line.Discount.Value, "0") || !orders.NativeTaxPreservesPrice(line, movement.VATEnabled, movement.VATIncluded) {
			return nativeAmountIssue("native_tax_or_discount_rounding_unverified")
		}
		price, scale, err := monetaryDecimal(line.Price)
		if err != nil {
			return nativeAmountIssue("native_sum_invalid")
		}
		quantity, err := parseDecimal(line.Quantity)
		if err != nil || quantity.coefficient.Sign() <= 0 {
			return nativeAmountIssue("native_sum_invalid")
		}
		amount := price.times(quantity)
		if !amount.rounded(scale).equal(amount) {
			return nativeAmountIssue("native_sum_rounding_unverified")
		}
	}
	return orders.Validation{}
}

func nativeAmountIssue(code string) orders.Validation {
	return orders.Validation{Field: "native_document.sum", Code: code}
}

func compareNativeTotal(reported orders.Field[orders.Money], calculated exactDecimal, currency string) orders.Validation {
	if reported.Validate() != nil || !reported.IsKnown() {
		return nativeAmountIssue("native_sum_unknown")
	}
	if reported.Value.Currency != currency {
		return nativeAmountIssue("currency_mismatch")
	}
	amount, _, err := monetaryDecimal(*reported.Value)
	if err != nil {
		return nativeAmountIssue("native_sum_invalid")
	}
	if !calculated.equal(amount) {
		return nativeAmountIssue("order_total_mismatch")
	}
	return orders.Validation{}
}
