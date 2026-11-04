package ordermoney

import "github.com/lilter96/styk/internal/orders"

// NativeDocumentTotal validates only the currently established simple native
// monetary contract. More complex provider discount/tax rounding is a precise
// capability blocker, not an invented accounting rule or an assigned sum.
func NativeDocumentTotal(command orders.Command, actual orders.NativeDocumentObservation) orders.Validation {
	movement := command.NativeMovement
	if movement == nil || len(movement.Lines) == 0 || !actual.Sum.IsKnown() {
		return orders.Validation{Field: "native_document.sum", Code: "native_sum_unknown"}
	}
	positions := make([]orders.PositionWrite, 0, len(movement.Lines))
	for _, line := range movement.Lines {
		if !line.Discount.IsKnown() || !orders.ExactDecimalEqual(*line.Discount.Value, "0") || !line.VAT.IsKnown() || !line.VATEnabled.IsKnown() || *line.VATEnabled.Value && !orders.ExactDecimalEqual(*line.VAT.Value, "0") {
			return orders.Validation{Field: "native_document.sum", Code: "native_tax_or_discount_rounding_unverified"}
		}
		positions = append(positions, orders.PositionWrite{Quantity: line.Quantity, Price: orders.Present(line.Price)})
	}
	total, currency, err := sumPositions(positions)
	if err != nil {
		return orders.Validation{Field: "native_document.sum", Code: "native_sum_invalid"}
	}
	validation := validateReportedTotal(actual.Sum, total, currency)
	if validation.Code != "" {
		validation.Field = "native_document.sum"
	}
	return validation
}
