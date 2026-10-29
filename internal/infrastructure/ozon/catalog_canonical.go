package ozon

import (
	"encoding/json"

	"github.com/lilter96/styk/internal/catalog"
)

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
