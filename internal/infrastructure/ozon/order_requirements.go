package ozon

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/lilter96/styk/internal/orders"
)

func decodeOrderRequirements(f orderFields) *orders.PackingRequirements {
	presence := f.objectPresence("requirements")
	if presence == orders.PresenceMissing {
		return nil
	}
	r := &orders.PackingRequirements{Presence: presence}
	if presence != orders.PresenceValue {
		return r
	}
	child := f.object("requirements")
	r.Products = make(map[orders.PackingRequirementKind]orders.Field[[]string], len(orders.PackingRequirementKinds()))
	known := make(map[string]bool, len(orders.PackingRequirementKinds()))
	for _, kind := range orders.PackingRequirementKinds() {
		key := "products_requiring_" + string(kind)
		known[key] = true
		r.Products[kind] = orderField(child, key, decodeRequirementSKUs)
	}
	for key := range child.raw {
		if !known[key] {
			r.UnknownFields = true
		}
	}
	if r.UnknownFields {
		*f.drift = append(*f.drift, orders.Drift{Path: f.path + ".requirements", Code: "unknown_packing_requirement"})
	}
	if r.Validate() != nil {
		f.fail()
	}
	return r
}

func decodeRequirementSKUs(data []byte) ([]string, error) {
	var raw []json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw == nil || len(raw) > orders.MaxRequirementSKUs {
		return nil, errOrderPayload
	}
	values := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, entry := range raw {
		sku, err := orderID(entry)
		if err != nil {
			return nil, errOrderPayload
		}
		id, err := strconv.ParseInt(sku, 10, 64)
		if err != nil || id <= 0 || seen[sku] {
			return nil, errOrderPayload
		}
		seen[sku] = true
		values = append(values, sku)
	}
	slices.Sort(values)
	return values, nil
}
