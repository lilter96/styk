package ordermoney

import (
	"errors"
	"fmt"

	"github.com/lilter96/styk/internal/orders"
)

// ProjectLines returns an all-or-nothing projection. Unknown price/currency and
// ambiguous mappings block projection rather than supplying zero or array-based
// identity. Quantity remains the observed quantity, without cancellation maths;
// reserve remains unspecified for the planner to authorize explicitly.
func ProjectLines(posting orders.Posting, mappings []orders.ProductMappingRef) ([]orders.PositionWrite, []orders.Validation) {
	if validation := validateLineSet(posting); validation.Code != "" {
		return nil, []orders.Validation{validation}
	}
	positions := make([]orders.PositionWrite, 0, len(posting.Lines))
	validations := make([]orders.Validation, 0)
	for index, line := range posting.Lines {
		position, validation := projectLine(posting.Scope, line, mappings)
		if validation.Code != "" {
			validation.Field = fmt.Sprintf("lines[%d].%s", index, validation.Field)
			validations = append(validations, validation)
			continue
		}
		positions = append(positions, position)
	}
	if len(validations) != 0 {
		return nil, validations
	}
	if validation := validateTotals(posting.Financial, positions); validation.Code != "" {
		return nil, []orders.Validation{validation}
	}
	return positions, nil
}

func validateLineSet(posting orders.Posting) orders.Validation {
	if posting.Scope.Validate() != nil {
		return orders.Validation{Field: "scope", Code: "invalid_scope"}
	}
	if len(posting.Lines) == 0 {
		return orders.Validation{Field: "lines", Code: "lines_unknown"}
	}
	identities := make([]orders.LineIdentity, 0, len(posting.Lines))
	for _, line := range posting.Lines {
		identities = append(identities, line.Identity)
	}
	err := orders.ValidateLineIdentities(identities)
	if errors.Is(err, orders.ErrAmbiguousLine) {
		return orders.Validation{Field: "lines", Code: "ambiguous_line_identity"}
	}
	if err != nil {
		return orders.Validation{Field: "lines", Code: "invalid_line_identity"}
	}
	return orders.Validation{}
}

func projectLine(scope orders.Scope, line orders.PostingLine, mappings []orders.ProductMappingRef) (orders.PositionWrite, orders.Validation) {
	if validation := validateSourceLine(line); validation.Code != "" {
		return orders.PositionWrite{}, validation
	}
	mapping, code := mappingForLine(scope, line, mappings)
	if code != "" {
		return orders.PositionWrite{}, orders.Validation{Field: "mapping", Code: code}
	}
	position := orders.PositionWrite{Identity: line.Identity, Mapping: mapping, Quantity: line.Quantity, Price: cloneField(line.Price), VAT: cloneField(line.VAT)}
	return position, orders.Validation{}
}

func validateSourceLine(line orders.PostingLine) orders.Validation {
	quantity, err := parseDecimal(line.Quantity)
	if err != nil || quantity.coefficient.Sign() <= 0 {
		return orders.Validation{Field: "quantity", Code: "invalid_quantity"}
	}
	withoutPrice := line
	withoutPrice.Price = orders.Field[orders.Money]{}
	if withoutPrice.Validate() != nil {
		return orders.Validation{Field: "identity", Code: "invalid_line"}
	}
	if line.Price.Validate() != nil {
		return orders.Validation{Field: "price", Code: "invalid_price"}
	}
	if !line.Price.IsKnown() {
		return orders.Validation{Field: "price", Code: "price_unknown"}
	}
	if line.Price.Value.Currency == "" {
		return orders.Validation{Field: "price.currency", Code: "currency_unknown"}
	}
	if _, err := MoneyToMinor(*line.Price.Value); err != nil {
		code := "invalid_price"
		if errors.Is(err, ErrUnsupportedCurrency) {
			code = "currency_unsupported"
		}
		return orders.Validation{Field: "price", Code: code}
	}
	return orders.Validation{}
}

func cloneField[T any](field orders.Field[T]) orders.Field[T] {
	if field.Value != nil {
		value := *field.Value
		field.Value = &value
	}
	return field
}

func mappingForLine(scope orders.Scope, line orders.PostingLine, mappings []orders.ProductMappingRef) (orders.ProductMappingRef, string) {
	var match orders.ProductMappingRef
	count := 0
	for _, mapping := range mappings {
		if mapping.Scope != scope || !mapping.Matches(line.Identity) {
			continue
		}
		if line.ProductID.IsKnown() && mapping.OzonProductID != *line.ProductID.Value {
			continue
		}
		match, count = mapping, count+1
	}
	if count > 1 {
		return orders.ProductMappingRef{}, "ambiguous_product_mapping"
	}
	if count == 0 {
		return orders.ProductMappingRef{}, "product_mapping_unknown"
	}
	return match, ""
}
