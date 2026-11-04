package orders

import (
	"encoding/json"
	"reflect"
	"slices"
)

// SplitObserved binds each returned posting to an approved package by its
// contents, never by response order. Child line IDs may legitimately differ
// from the original line IDs; SKU and offer remain separate identities.
func SplitObserved(before Posting, packages []ActionPackage, numbers []string, children []Posting) bool {
	if len(packages) < 2 || len(packages) > 1000 || len(numbers) != len(packages) || len(children) != len(numbers) {
		return false
	}
	wanted, ok := splitPackages(packages)
	if !ok {
		return false
	}
	seen := make(map[string]bool, len(children))
	for _, child := range children {
		if !splitChildValid(before, child) || seen[child.PostingNumber] || !slices.Contains(numbers, child.PostingNumber) {
			return false
		}
		seen[child.PostingNumber] = true
		key, ok := splitContents(child.Lines)
		if !ok || wanted[key] == 0 {
			return false
		}
		wanted[key]--
	}
	return splitConserved(before, packages)
}

func splitChildValid(before, child Posting) bool {
	if child.Validate() != nil || child.Evidence.Origin != OriginOzon || child.Scope != before.Scope || child.CommercialOrderID != before.CommercialOrderID {
		return false
	}
	if !reflect.DeepEqual(before.WarehouseID, child.WarehouseID) || !reflect.DeepEqual(before.DeliveryMethodID, child.DeliveryMethodID) {
		return false
	}
	if !child.Status.IsKnown() || !slices.Contains([]string{"awaiting_deliver", "awaiting_registration", "delivering", "driver_pickup", "delivered"}, *child.Status.Value) {
		return false
	}
	return !child.Substatus.IsKnown() || *child.Substatus.Value != "ship_failed"
}

func SplitChildObserved(before, child Posting, packages []ActionPackage) bool {
	if !splitChildValid(before, child) {
		return false
	}
	key, ok := splitContents(child.Lines)
	if !ok {
		return false
	}
	wanted, ok := splitPackages(packages)
	return ok && wanted[key] > 0
}

func splitContents(lines []PostingLine) (string, bool) {
	units := make(map[string]int64, len(lines))
	if len(lines) == 0 {
		return "", false
	}
	for _, line := range lines {
		quantity, ok := fulfillmentQuantity(line.Quantity)
		key := identityPart(line.Identity.SKU) + identityPart(line.Identity.OfferID)
		if !ok || line.Identity.Validate() != nil || units[key] != 0 {
			return "", false
		}
		units[key] = quantity
	}
	encoded, err := json.Marshal(units)
	return string(encoded), err == nil
}

func splitPackages(packages []ActionPackage) (map[string]int, bool) {
	result := make(map[string]int, len(packages))
	for _, box := range packages {
		lines := make([]PostingLine, 0, len(box.Lines))
		for _, line := range box.Lines {
			lines = append(lines, PostingLine{Identity: line.Identity, Quantity: line.Quantity})
		}
		key, ok := splitContents(lines)
		if !ok {
			return nil, false
		}
		result[key]++
	}
	return result, true
}

func splitConserved(before Posting, packages []ActionPackage) bool {
	remaining, ok := fulfillmentUnits(before.Lines)
	if !ok {
		return false
	}
	for _, box := range packages {
		for _, line := range box.Lines {
			key := line.Identity.Key()
			quantity, valid := fulfillmentQuantity(line.Quantity)
			if !valid || remaining[key] < quantity {
				return false
			}
			remaining[key] -= quantity
		}
	}
	for _, quantity := range remaining {
		if quantity != 0 {
			return false
		}
	}
	return true
}
