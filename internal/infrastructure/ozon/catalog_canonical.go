package ozon

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lilter96/styk/internal/catalog"
)

// Attribute entries have their own IDs; provider enumeration order is not a
// product change. Keep every entry and its value sequence, including unknown facts.
func stableCatalogAttributes(wire catalogWire) (catalogWire, error) {
	result := make(catalogWire, len(wire))
	for key, value := range wire {
		result[key] = value
	}
	for _, key := range []string{"attributes", "complex_attributes", "attributes_with_defaults"} {
		value, ok := wire[key]
		if !ok || string(value) == "null" {
			continue
		}
		ordered, err := stableCatalogEntries(value)
		if err != nil {
			return nil, fmt.Errorf("canonicalize catalog attribute collection: %w", err)
		}
		result[key] = ordered
	}
	return result, nil
}

func stableCatalogEntries(data json.RawMessage) (json.RawMessage, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, ErrCatalogContract
	}
	type entry struct {
		data json.RawMessage
		key  string
	}
	ordered := make([]entry, 0, len(entries))
	for _, value := range entries {
		key, err := catalog.FingerprintValue(catalog.Value{State: catalog.StateKnown, Data: value})
		if err != nil {
			return nil, fmt.Errorf("fingerprint catalog attribute: %w", err)
		}
		ordered = append(ordered, entry{data: value, key: key})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].key < ordered[j].key })
	result := make([]json.RawMessage, 0, len(ordered))
	for _, value := range ordered {
		result = append(result, value.data)
	}
	return json.Marshal(result)
}

func catalogUnownedAttributes(data json.RawMessage) catalog.Value {
	if len(data) == 0 {
		return catalog.Value{State: catalog.StateAbsent}
	}
	if string(data) == "null" {
		return catalog.Value{State: catalog.StateUnknown}
	}
	var attributes []json.RawMessage
	if json.Unmarshal(data, &attributes) != nil {
		return catalog.Value{State: catalog.StateUnknown}
	}
	selected := make([]json.RawMessage, 0, len(attributes))
	for _, attribute := range attributes {
		var identity struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(attribute, &identity) != nil {
			return catalog.Value{State: catalog.StateUnknown}
		}
		if identity.ID != 4180 && identity.ID != 4191 {
			selected = append(selected, attribute)
		}
	}
	if len(selected) == 0 {
		return catalog.Empty()
	}
	return catalogRaw(selected)
}

// Canonical fields carry these known facts once. Unknown provider facts are
// retained separately; timestamps and moderation diagnostics are observations
// of processing, rather than independently owned product attributes.
func catalogInfoExtras(wire catalogWire) catalog.Value {
	return catalogExtras(wire, "id", "sku", "offer_id", "name", "price", "currency_code", "vat", "barcodes", "images", "is_archived", "description_category_id", "type_id", "created_at", "updated_at", "statuses", "errors")
}

func catalogAttributeExtras(wire catalogWire) catalog.Value {
	return catalogExtras(wire, "id", "sku", "offer_id", "name", "attributes", "attributes_with_defaults", "description_category_id", "type_id", "height", "depth", "width", "dimension_unit", "weight", "weight_unit", "barcode", "barcodes")
}

func catalogExtras(wire catalogWire, known ...string) catalog.Value {
	remaining := make(catalogWire, len(wire))
	for key, value := range wire {
		remaining[key] = value
	}
	for _, key := range known {
		delete(remaining, key)
	}
	return catalogRaw(remaining)
}
