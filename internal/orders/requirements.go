package orders

import (
	"slices"
	"strconv"
)

type PackingRequirementKind string

const (
	RequirementCountry       PackingRequirementKind = "country"
	RequirementChangeCountry PackingRequirementKind = "change_country"
	RequirementGTD           PackingRequirementKind = "gtd"
	RequirementMandatoryMark PackingRequirementKind = "mandatory_mark"
	RequirementJewelryUIN    PackingRequirementKind = "jw_uin"
	RequirementRNPT          PackingRequirementKind = "rnpt"
	RequirementIMEI          PackingRequirementKind = "imei"
	RequirementWeight        PackingRequirementKind = "weight"
)

const MaxRequirementSKUs = 1000

func PackingRequirementKinds() []PackingRequirementKind {
	return []PackingRequirementKind{RequirementCountry, RequirementChangeCountry, RequirementGTD, RequirementMandatoryMark,
		RequirementJewelryUIN, RequirementRNPT, RequirementIMEI, RequirementWeight}
}

// PackingRequirements contains requirements returned by Ozon, never regulatory
// guesses. An explicitly empty SKU list means no outstanding requirement of that
// kind; absent/null lists remain unknown. Values are Ozon SKUs, not product IDs.
type PackingRequirements struct {
	Presence      Presence                                   `json:"presence"`
	Products      map[PackingRequirementKind]Field[[]string] `json:"products"`
	UnknownFields bool                                       `json:"unknown_fields,omitempty"`
}

func (r PackingRequirements) Validate() error {
	if r.Presence != PresenceValue && r.Presence != PresenceEmpty && r.Presence != PresenceNull && r.Presence != PresenceMissing {
		return ErrInvalid
	}
	if len(r.Products) > len(PackingRequirementKinds()) || (r.Presence != PresenceValue && len(r.Products) != 0) {
		return ErrInvalid
	}
	for kind, field := range r.Products {
		if !slices.Contains(PackingRequirementKinds(), kind) || field.Validate() != nil {
			return ErrInvalid
		}
		if !field.IsKnown() {
			continue
		}
		if len(*field.Value) > MaxRequirementSKUs {
			return ErrInvalid
		}
		seen := make(map[string]bool, len(*field.Value))
		for _, sku := range *field.Value {
			id, err := strconv.ParseInt(sku, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != sku || seen[sku] {
				return ErrInvalid
			}
			seen[sku] = true
		}
	}
	return nil
}

func (r *PackingRequirements) Canonical() *PackingRequirements {
	if r == nil {
		return nil
	}
	copy := *r
	copy.Products = make(map[PackingRequirementKind]Field[[]string], len(r.Products))
	for kind, field := range r.Products {
		if field.IsKnown() && field.Value != nil {
			values := slices.Clone(*field.Value)
			slices.Sort(values)
			field.Value = &values
		}
		copy.Products[kind] = field
	}
	return &copy
}

// MergePackingRequirements keeps known display evidence when a shortened
// response omits it. Explicit empty lists DO replace old requirements. Worker
// dispatch still compares the review with a fresh complete provider response.
func MergePackingRequirements(previous, next *PackingRequirements) *PackingRequirements {
	if next == nil {
		return previous.Canonical()
	}
	if previous == nil {
		return next.Canonical()
	}
	if next.Presence == PresenceMissing || next.Presence == PresenceNull || next.Presence == PresenceEmpty {
		return previous.Canonical()
	}
	result := next.Canonical()
	for kind, field := range previous.Products {
		current := result.Products[kind]
		if current.IsMissing() || current.Presence == PresenceNull {
			result.Products[kind] = field
		}
	}
	return result.Canonical()
}
