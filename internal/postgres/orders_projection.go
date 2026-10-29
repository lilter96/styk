package postgres

import (
	"encoding/json"

	"github.com/lilter96/styk/internal/orders"
)

// Projection keeps the last known typed fact when a later snapshot supplies no
// value. The immutable observation retains the provider's actual presence.
func mergeOrderPosting(previous, next orders.Posting) (orders.Posting, error) {
	before, err := orderProjectionJSON(previous)
	if err != nil {
		return orders.Posting{}, err
	}
	after, err := orderProjectionJSON(next)
	if err != nil {
		return orders.Posting{}, err
	}
	raw, err := ordersJSON(mergeOrderJSON(before, after))
	if err != nil {
		return orders.Posting{}, err
	}
	var result orders.Posting
	if err = ordersDecode(raw, &result); err != nil {
		return orders.Posting{}, err
	}
	result.Evidence = next.Evidence
	if next.DeliveryProfile == nil {
		result.DeliveryProfile = previous.DeliveryProfile
	}
	return result, nil
}

func orderProjectionJSON(value any) (any, error) {
	raw, err := ordersJSON(value)
	if err != nil {
		return nil, err
	}
	var result any
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, orders.ErrInvalid
	}
	return result, nil
}

func mergeOrderJSON(before, after any) any {
	oldObject, oldMap := before.(map[string]any)
	newObject, newMap := after.(map[string]any)
	if oldMap && newMap {
		if oldPresence, ok := oldObject["presence"].(string); ok {
			newPresence, _ := newObject["presence"].(string)
			if (oldPresence == "value" || oldPresence == "zero") && newPresence != "value" && newPresence != "zero" {
				return oldObject
			}
		}
		for key, value := range newObject {
			newObject[key] = mergeOrderJSON(oldObject[key], value)
		}
		return newObject
	}
	oldArray, oldSlice := before.([]any)
	newArray, newSlice := after.([]any)
	if oldSlice && newSlice {
		return mergeOrderLineJSON(oldArray, newArray)
	}
	return after
}

func mergeOrderLineJSON(before, after []any) []any {
	oldLines := make(map[string]any, len(before))
	for _, line := range before {
		if key := orderLineJSONKey(line); key != "" {
			oldLines[key] = line
		}
	}
	for index, line := range after {
		if key := orderLineJSONKey(line); key != "" {
			after[index] = mergeOrderJSON(oldLines[key], line)
		}
	}
	return after
}

func orderLineJSONKey(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	identity, ok := object["identity"].(map[string]any)
	if !ok {
		return ""
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	return string(raw)
}
