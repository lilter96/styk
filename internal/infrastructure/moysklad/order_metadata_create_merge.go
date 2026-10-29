package moysklad

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/lilter96/styk/internal/orders"
)

// Native attributes have collection semantics. The narrow update path sends
// every observed STYK string/text value, preserving omitted values by UUID.
// Any unowned or complex value blocks the write; raw values remain transient.
func (c *OrderClient) mergeOrderAttributeUpdate(ctx context.Context, command orders.Command, body map[string]any, metadata OrderMetadata) error {
	if command.Action != orders.ActionMirrorUpdate {
		return orders.ErrInvalid
	}
	current, err := c.orderAttributeUpdateSnapshot(ctx, command)
	if err != nil {
		return err
	}
	merged, err := c.orderCurrentAttributeValues(current)
	if err != nil {
		return err
	}
	if err = mergeOwnedOrderAttributes(command, merged, metadata); err != nil {
		return err
	}
	if len(merged) > 100 {
		return orders.ErrBlocked
	}
	ids := make([]string, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	attributes := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		attributes = append(attributes, map[string]any{"meta": c.reference("entity/customerorder/metadata/attributes", "attributemetadata", id).Meta, "value": merged[id]})
	}
	body["attributes"] = attributes
	_, err = c.validateOrderPayload(command, body)
	return err
}

func mergeOwnedOrderAttributes(command orders.Command, merged map[string]string, metadata OrderMetadata) error {
	owned, err := orderOwnedFields(command)
	if err != nil {
		return err
	}
	for _, attribute := range command.CustomerOrder.Attributes {
		fieldOwned := owned["attribute"] || owned["attributes"] || owned["attribute:"+attribute.AttributeID]
		if !attribute.Value.IsKnown() || !fieldOwned {
			continue
		}
		if !orderManagedAttributeDefinition(metadata.Attributes, attribute.AttributeID) {
			return orders.ErrBlocked
		}
		merged[attribute.AttributeID] = *attribute.Value.Value
	}
	return nil
}

func (c *OrderClient) orderAttributeUpdateSnapshot(ctx context.Context, command orders.Command) (json.RawMessage, error) {
	path := "entity/customerorder/" + command.Correlation.CustomerOrderID
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	document, fields, err := c.decodeOrderIdentity(raw, command.Correlation)
	if err != nil {
		return nil, err
	}
	observation, err := c.decodeOrderFields(ctx, document, fields, command.Correlation)
	if err != nil {
		return nil, err
	}
	rows, err := c.readOrderPositions(ctx, document, observation.Fields.CurrencyID, observation.Sum)
	if err != nil {
		return nil, err
	}
	observation, err = c.finishOrderRead(ctx, path, fields, rows, observation)
	if err != nil {
		return nil, err
	}
	if !observation.Complete || observation.Revision != command.ExpectedTargetRevision {
		return nil, orders.ErrStale
	}
	return fields["attributes"], nil
}

func (c *OrderClient) orderCurrentAttributeValues(raw json.RawMessage) (map[string]string, error) {
	values := make(map[string]string)
	if len(raw) == 0 {
		return values, nil
	}
	var attributes []struct {
		catalogReference
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if string(raw) == "null" || json.Unmarshal(raw, &attributes) != nil || len(attributes) > 100 {
		return nil, orders.ErrBlocked
	}
	for _, attribute := range attributes {
		if !validOrderAttributeMarker(attribute.Name) || (attribute.Type != "string" && attribute.Type != "text") {
			return nil, orders.ErrBlocked
		}
		id, err := c.referenceID(attribute.catalogReference, "entity/customerorder/metadata/attributes", "attributemetadata")
		if err != nil || attribute.ID != id {
			return nil, orders.ErrBlocked
		}
		if _, exists := values[id]; exists {
			return nil, orders.ErrConflict
		}
		if len(attribute.Value) == 0 || string(attribute.Value) == "null" {
			return nil, orders.ErrBlocked
		}
		var value string
		if json.Unmarshal(attribute.Value, &value) != nil {
			return nil, orders.ErrBlocked
		}
		values[id] = value
	}
	return values, nil
}

func orderManagedAttributeDefinition(attributes []OrderAttribute, id string) bool {
	for _, attribute := range attributes {
		if attribute.ID == id {
			return validOrderAttributeMarker(attribute.Name) && (attribute.Type == "string" || attribute.Type == "text")
		}
	}
	return false
}
