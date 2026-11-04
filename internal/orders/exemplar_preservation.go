package orders

func validObservedBoxCount(value Field[int64]) bool {
	return value.Validate() == nil && (!value.IsKnown() || *value.Value >= 0 && *value.Value <= 2147483647)
}

// An omitted fresh weight is never proof that a previously observed weight is
// unchanged, including the provider's explicit zero placeholder.
func observedExemplarWeightEqual(expected, actual Field[Decimal]) bool {
	if expected.Validate() != nil || actual.Validate() != nil {
		return false
	}
	if expected.IsKnown() || actual.IsKnown() {
		return expected.IsKnown() && actual.IsKnown() && ExactDecimalEqual(*expected.Value, *actual.Value)
	}
	return expected.Presence == actual.Presence || expected.IsMissing() && actual.IsMissing()
}

// v5 status does not provide box grouping. The existing fresh posting detail
// read is a separate source; never label its evidence as a v5 response field.
func exemplarBoxPreservationValidation(snapshot ExemplarSnapshot, posting Posting) []Validation {
	result := make([]Validation, 0)
	if snapshot.MultiBoxQty.IsKnown() || posting.MultiBoxQty.IsKnown() {
		if !snapshot.MultiBoxQty.IsKnown() || !posting.MultiBoxQty.IsKnown() {
			return append(result, Validation{Field: "multi_box_qty", Code: "exemplar_multibox_fresh_evidence_required"})
		}
		if *snapshot.MultiBoxQty.Value != *posting.MultiBoxQty.Value {
			return append(result, Validation{Field: "multi_box_qty", Code: "exemplar_multibox_changed"})
		}
	}
	return result
}

func ExemplarPreservationInputValidation(input ExemplarSetInput) []Validation {
	result := make([]Validation, 0)
	if input.MultiBoxQty.IsSpecified() {
		result = append(result, Validation{Field: "multi_box_qty", Code: "exemplar_grouping_merchant_edit_not_supported"})
	}
	for _, product := range input.Products {
		for _, unit := range product.Exemplars {
			if unit.Weight.IsSpecified() {
				result = append(result, Validation{Field: "products[sku=" + product.SKU + "].exemplar[" + unit.ID + "].weight", Code: "exemplar_weight_merchant_edit_not_supported"})
			}
		}
	}
	return result
}

func ExemplarWeightReadbackCode(expected ExemplarSnapshot, status ExemplarStatus) string {
	for _, product := range expected.Products {
		for _, unit := range product.Exemplars {
			if !unit.Weight.IsKnown() {
				continue
			}
			var observed Field[Decimal]
			for _, actual := range status.Products {
				if actual.SKU != product.SKU {
					continue
				}
				for _, candidate := range actual.Exemplars {
					if candidate.ID == unit.ID {
						observed = candidate.Weight
					}
				}
			}
			if !observed.IsKnown() {
				return "EXEMPLAR_WEIGHT_FRESH_READBACK_REQUIRED"
			}
			if !observedExemplarWeightEqual(unit.Weight, observed) {
				return "EXEMPLAR_PRESERVED_WEIGHT_CHANGED"
			}
		}
	}
	return "EXEMPLAR_VALUES_OR_VALIDATION_PENDING"
}
