package orders

import (
	"reflect"
	"strconv"
)

// PartialCancellationObserved requires the exact approved reduction of the
// current detail quantities. It never interprets a changed array or a posting
// status as cancellation evidence, nor subtracts a reserve a second time.
func PartialCancellationObserved(before, after Posting, lines []ActionQuantity) bool {
	if !sameFulfillmentTarget(before, after) || len(lines) == 0 {
		return false
	}
	wanted, ok := fulfillmentUnits(before.Lines)
	if !ok {
		return false
	}
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		key := line.Identity.Key()
		quantity, valid := fulfillmentQuantity(line.Quantity)
		if !valid || seen[key] || wanted[key] < quantity {
			return false
		}
		seen[key] = true
		wanted[key] -= quantity
		if wanted[key] == 0 {
			delete(wanted, key)
		}
	}
	actual, ok := fulfillmentUnits(after.Lines)
	return ok && reflect.DeepEqual(wanted, actual)
}

func sameFulfillmentTarget(before, after Posting) bool {
	return before.Evidence.Origin == OriginOzon && after.Evidence.Origin == OriginOzon && before.WarehouseID.IsKnown() && before.DeliveryMethodID.IsKnown() && before.Scope == after.Scope && before.PostingNumber == after.PostingNumber && before.CommercialOrderID == after.CommercialOrderID && reflect.DeepEqual(before.WarehouseID, after.WarehouseID) && reflect.DeepEqual(before.DeliveryMethodID, after.DeliveryMethodID)
}

func fulfillmentUnits(lines []PostingLine) (map[string]int64, bool) {
	units := make(map[string]int64, len(lines))
	for _, line := range lines {
		quantity, ok := fulfillmentQuantity(line.Quantity)
		key := line.Identity.Key()
		if !ok || line.Identity.Validate() != nil || units[key] != 0 {
			return nil, false
		}
		units[key] = quantity
	}
	return units, true
}

func fulfillmentQuantity(quantity Decimal) (int64, bool) {
	value, err := strconv.ParseInt(string(quantity), 10, 32)
	return value, err == nil && value > 0 && strconv.FormatInt(value, 10) == string(quantity)
}
