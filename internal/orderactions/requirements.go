package orderactions

import (
	"github.com/lilter96/styk/internal/orders"
)

// PackingRequirementValidation gates only packing/splitting. A problem with a
// marked product never blocks reading, mirroring or unrelated postings. The
// reviewed ship-v4 grammar cannot carry exemplar/marking values inline.
func PackingRequirementValidation(posting orders.Posting, action orders.Action) []orders.Validation {
	if action != orders.ActionPostingPack && action != orders.ActionPostingSplit {
		return nil
	}
	r := posting.Requirements
	if r == nil || r.Validate() != nil || r.Presence != orders.PresenceValue {
		return []orders.Validation{actionBlock("requirements", "packing_requirements_not_observed")}
	}
	result := make([]orders.Validation, 0)
	if r.UnknownFields {
		result = append(result, actionBlock("requirements", "packing_requirement_contract_changed"))
	}
	skus := make(map[string]bool, len(posting.Lines))
	for _, line := range posting.Lines {
		skus[line.Identity.SKU] = true
	}
	for _, kind := range orders.PackingRequirementKinds() {
		field := r.Products[kind]
		if field.Presence == orders.PresenceEmpty {
			continue
		}
		if !field.IsKnown() {
			result = append(result, actionBlock("requirements."+string(kind), "packing_requirement_unknown"))
			continue
		}
		result = append(result, packingProductRequirements(kind, *field.Value, skus)...)
	}
	return result
}

func packingProductRequirements(kind orders.PackingRequirementKind, values []string, skus map[string]bool) []orders.Validation {
	result := make([]orders.Validation, 0, len(values))
	for _, sku := range values {
		code := "packing_" + string(kind) + "_required"
		if !skus[sku] {
			code = "packing_requirement_sku_not_in_posting"
		}
		result = append(result, actionBlock("products[sku="+sku+"].requirements."+string(kind), code))
	}
	return result
}
