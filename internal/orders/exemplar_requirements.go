package orders

import "slices"

func CompleteExemplarRequirements(s *ExemplarSnapshot, p Posting) {
	for i := range s.Products {
		product := &s.Products[i]
		if product.Required == nil {
			product.Required = make(map[PackingRequirementKind]Field[bool])
		}
		if p.Requirements == nil {
			continue
		}
		for _, kind := range PackingRequirementKinds() {
			field := p.Requirements.Products[kind]
			if field.IsKnown() && slices.Contains(*field.Value, product.SKU) {
				product.Required[kind] = Present(true)
				continue
			}
			if product.Required[kind].IsKnown() {
				continue
			}
			if field.Presence == PresenceEmpty || field.IsKnown() {
				product.Required[kind] = Present(false)
			}
		}
	}
}

func ValidateExemplarSet(s ExemplarSnapshot, p Posting) []Validation {
	if s.ValidateFor(p) != nil {
		return []Validation{{Field: "exemplars", Code: "exemplar_target_or_units_changed"}}
	}
	result := make([]Validation, 0)
	for _, product := range s.Products {
		for _, u := range product.Exemplars {
			if !exemplarPrivateComplete(u) {
				result = append(result, Validation{Field: "products[sku=" + product.SKU + "].exemplar[" + u.ID + "]", Code: "exemplar_base_values_unknown"})
			}
		}
		for _, kind := range PackingRequirementKinds() {
			flag := product.Required[kind]
			field := "products[sku=" + product.SKU + "]." + string(kind)
			if !flag.IsKnown() {
				result = append(result, Validation{Field: field, Code: "exemplar_requirement_unknown"})
				continue
			}
			if !*flag.Value {
				continue
			}
			if kind == RequirementWeight {
				result = append(result, Validation{Field: field, Code: "exemplar_weight_unit_unverified"})
				continue
			}
			if kind == RequirementCountry || kind == RequirementChangeCountry {
				result = append(result, Validation{Field: field, Code: "exemplar_country_operation_required"})
				continue
			}
			for _, unit := range product.Exemplars {
				if !exemplarRequiredValue(unit, kind) {
					result = append(result, Validation{Field: field + ".exemplar[" + unit.ID + "]", Code: "exemplar_factual_value_required"})
				}
			}
		}
	}
	return result
}

func exemplarPrivateComplete(u ExemplarInput) bool {
	if !u.Marks.IsKnown() && u.Marks.Presence != PresenceEmpty {
		return false
	}
	for _, pair := range []struct {
		value  Field[string]
		absent Field[bool]
	}{{u.GTD, u.GTDAbsent}, {u.RNPT, u.RNPTAbsent}} {
		if !pair.absent.IsKnown() {
			return false
		}
		if !pair.value.IsKnown() && pair.value.Presence != PresenceEmpty && !*pair.absent.Value {
			return false
		}
	}
	return true
}

func exemplarRequiredValue(u ExemplarInput, kind PackingRequirementKind) bool {
	switch kind {
	case RequirementGTD:
		return u.GTD.IsKnown() || (u.GTDAbsent.IsKnown() && *u.GTDAbsent.Value)
	case RequirementRNPT:
		return u.RNPT.IsKnown() || (u.RNPTAbsent.IsKnown() && *u.RNPTAbsent.Value)
	default:
		return u.Marks.IsKnown() && slices.ContainsFunc(*u.Marks.Value, func(m ExemplarMark) bool { return m.Type == string(kind) })
	}
}

func ExemplarIDs(s ExemplarSnapshot) map[string][]string {
	result := make(map[string][]string, len(s.Products))
	for _, p := range s.Products {
		ids := make([]string, 0, len(p.Exemplars))
		for _, u := range p.Exemplars {
			ids = append(ids, u.ID)
		}
		slices.Sort(ids)
		result[p.SKU] = ids
	}
	return result
}
