package moysklad

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

var shipmentAddressNativeKeys = map[string]string{"city": "city", "street": "street", "house": "house", "apartment": "apartment", "postal_code": "postalCode", "add_info": "addInfo", "comment": "comment", "country_id": "country", "region_id": "region"}

func (c *OrderClient) readShipmentAddressFull(raw json.RawMessage) (orders.Field[orders.ShipmentAddress], error) {
	if len(raw) == 0 {
		return orders.Field[orders.ShipmentAddress]{Presence: orders.PresenceMissing}, nil
	}
	if string(raw) == "null" {
		return orders.Field[orders.ShipmentAddress]{Presence: orders.PresenceNull}, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return orders.Field[orders.ShipmentAddress]{}, ErrOrderSchema
	}
	if !shipmentAddressKnownKeys(fields) {
		return orders.Field[orders.ShipmentAddress]{}, orders.ErrSchemaDrift
	}
	result := orders.ShipmentAddress{}
	for key, native := range shipmentAddressNativeKeys {
		value, err := orderReadField[string](fields, native)
		if key == "country_id" || key == "region_id" {
			value, err = c.readShipmentAddressReference(fields[native], strings.TrimSuffix(key, "_id"))
		}
		if err != nil {
			return orders.Field[orders.ShipmentAddress]{}, err
		}
		result.Set(key, value)
	}
	if result.Validate() != nil {
		return orders.Field[orders.ShipmentAddress]{}, ErrOrderSchema
	}
	return orders.Present(result), nil
}
func shipmentAddressKnownKeys(fields map[string]json.RawMessage) bool {
	for key := range fields {
		found := false
		for _, native := range shipmentAddressNativeKeys {
			found = found || key == native
		}
		if !found {
			return false
		}
	}
	return true
}
func (c *OrderClient) readShipmentAddressReference(raw json.RawMessage, kind string) (orders.Field[string], error) {
	if len(raw) == 0 {
		return orders.Field[string]{Presence: orders.PresenceMissing}, nil
	}
	if string(raw) == "null" {
		return orders.Field[string]{Presence: orders.PresenceNull}, nil
	}
	var reference catalogReference
	if json.Unmarshal(raw, &reference) != nil {
		return orders.Field[string]{}, ErrOrderSchema
	}
	id, err := c.referenceID(reference, "entity/"+kind, kind)
	if err != nil {
		return orders.Field[string]{}, err
	}
	return orders.Present(id), nil
}
func addShipmentAddressFull(body map[string]any, write orders.CustomerOrderWrite, owned map[string]bool) error {
	if !write.ShipmentAddressFull.IsKnown() {
		return nil
	}
	if !write.HasShipmentAddressFullMapping() || write.HasShipmentAddressMapping() || write.ShipmentAddressFull.Value.Validate() != nil {
		return orders.ErrInvalid
	}
	result := make(map[string]any)
	for key, value := range write.ShipmentAddressFull.Value.Fields() {
		if !owned["shipment_address_full."+key] {
			continue
		}
		if value.IsKnown() {
			result[shipmentAddressNativeKeys[key]] = *value.Value
		}
		if value.Presence == orders.PresenceEmpty {
			result[shipmentAddressNativeKeys[key]] = ""
		}
	}
	if len(result) > 0 {
		body["shipmentAddressFull"] = result
		delete(body, "shipmentAddress")
	}
	return nil
}
func (c *OrderClient) mergeShipmentAddressFullUpdate(ctx context.Context, command orders.Command, body map[string]any) error {
	patch, ok := body["shipmentAddressFull"].(map[string]any)
	if !ok || command.Action != orders.ActionMirrorUpdate {
		return nil
	}
	target, err := c.readCustomerOrder(ctx, command.Correlation)
	if err != nil {
		return err
	}
	if !target.Complete || target.Revision != command.ExpectedTargetRevision {
		return orders.ErrStale
	}
	if target.Fields.ShipmentAddressFull.Presence == orders.PresenceMissing || target.Fields.ShipmentAddressFull.Presence == orders.PresenceNull {
		if target.Fields.ShipmentAddress.IsKnown() {
			return orders.ErrBlocked
		}
		return nil
	}
	if !target.Fields.ShipmentAddressFull.IsKnown() {
		return orders.ErrBlocked
	}
	merged := c.shipmentAddressFullPayload(*target.Fields.ShipmentAddressFull.Value)
	for key, value := range patch {
		merged[key] = value
	}
	body["shipmentAddressFull"] = merged
	return nil
}
func (c *OrderClient) shipmentAddressFullPayload(value orders.ShipmentAddress) map[string]any {
	result := make(map[string]any)
	for key, field := range value.Fields() {
		if field.Presence == orders.PresenceEmpty {
			result[shipmentAddressNativeKeys[key]] = ""
			continue
		}
		if !field.IsKnown() {
			continue
		}
		if key == "country_id" || key == "region_id" {
			kind := strings.TrimSuffix(key, "_id")
			result[shipmentAddressNativeKeys[key]] = c.reference("entity/"+kind, kind, *field.Value)
			continue
		}
		result[shipmentAddressNativeKeys[key]] = *field.Value
	}
	return result
}
