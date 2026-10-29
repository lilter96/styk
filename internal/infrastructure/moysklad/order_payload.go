package moysklad

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func (c *OrderClient) orderPayload(command orders.Command) (map[string]any, error) {
	if !c.scopeAllowed(command.Scope) || command.Validate() != nil || !command.Action.IsMirror() {
		return nil, ErrOrderIntent
	}
	if transport, coordinated := c.client.http.Transport.(providercall.Transport); coordinated && (!orderTransportScope(transport, command.Scope) || transport.Scope.CredentialRevision != command.Credentials.Moysklad) {
		return nil, ErrOrderIntent
	}
	write := command.CustomerOrder
	if err := orderWriteReferences(*write); err != nil {
		return nil, err
	}
	owned, err := orderOwnedFields(command)
	if err != nil {
		return nil, err
	}
	body := make(map[string]any)
	c.addOrderReferences(body, *write, owned)
	if err = addOrderFields(body, *write, owned); err != nil {
		return nil, err
	}
	if err = c.addOrderCollections(body, command, owned); err != nil {
		return nil, err
	}
	if command.Action == orders.ActionMirrorCreate {
		body["syncId"], body["externalCode"] = command.Correlation.SyncID, command.Correlation.Marker
	}
	if len(body) == 0 {
		return nil, orders.ErrBlocked
	}
	return c.validateOrderPayload(command, body)
}

func orderWriteReferences(write orders.CustomerOrderWrite) error {
	for _, id := range []string{write.OrganizationID, write.AgentID, write.StoreID, write.StateID, write.SalesChannelID, write.CurrencyID} {
		if !orderUUID.MatchString(id) {
			return ErrOrderIdentity
		}
	}
	if write.RateValue.Validate() != nil {
		return orders.ErrInvalid
	}
	if write.RateValue.IsKnown() && (write.RateValue.Value.Validate() != nil || strings.HasPrefix(string(*write.RateValue.Value), "-") || *write.RateValue.Value == "0") {
		return orders.ErrInvalid
	}
	return nil
}

func orderOwnedFields(command orders.Command) (map[string]bool, error) {
	owned := make(map[string]bool, len(command.CustomerOrder.OwnedFields))
	for _, field := range command.CustomerOrder.OwnedFields {
		if command.Action == orders.ActionMirrorUpdate && strings.HasPrefix(field, "positions.") {
			return nil, orders.ErrBlocked
		}
		if !orderWritableField(field) || owned[field] {
			return nil, orders.ErrInvalid
		}
		owned[field] = true
	}
	if command.Action == orders.ActionMirrorCreate {
		for _, field := range []string{"organization", "agent", "store", "state", "sales_channel", "rate", "name", "description", "moment", "delivery_at", "applicable", "vat_enabled", "vat_included", "positions", "attributes"} {
			owned[field] = true
		}
		return owned, nil
	}
	if owned["organization"] || owned["agent"] {
		// Their account references must be changed together; the command has no
		// reviewed organizationAccount or agentAccount mutation representation.
		return nil, orders.ErrBlocked
	}
	return owned, nil
}

func orderWritableField(field string) bool {
	switch field {
	case "organization", "agent", "store", "state", "sales_channel", "rate", "name", "description", "moment", "delivery_at", "applicable", "vat_enabled", "vat_included", "positions", "attributes", "attribute":
		return true
	case "positions.quantity", "positions.reserve", "positions.price", "positions.vat":
		return true
	}
	return strings.HasPrefix(field, "attribute:") && orderUUID.MatchString(strings.TrimPrefix(field, "attribute:"))
}

func (c *OrderClient) addOrderReferences(body map[string]any, write orders.CustomerOrderWrite, owned map[string]bool) {
	refs := []struct{ field, native, path, kind, id string }{
		{"organization", "organization", "entity/organization", "organization", write.OrganizationID},
		{"agent", "agent", "entity/counterparty", "counterparty", write.AgentID},
		{"store", "store", "entity/store", "store", write.StoreID},
		{"state", "state", "entity/customerorder/metadata/states", "state", write.StateID},
		{"sales_channel", "salesChannel", "entity/saleschannel", "saleschannel", write.SalesChannelID},
	}
	for _, ref := range refs {
		if owned[ref.field] {
			body[ref.native] = c.reference(ref.path, ref.kind, ref.id)
		}
	}
	if owned["rate"] {
		rate := map[string]any{"currency": c.reference("entity/currency", "currency", write.CurrencyID)}
		if write.RateValue.IsKnown() {
			rate["value"] = json.Number(*write.RateValue.Value)
		}
		body["rate"] = rate
	}
}

func addOrderFields(body map[string]any, write orders.CustomerOrderWrite, owned map[string]bool) error {
	orderAddField(body, "name", write.Name, owned["name"])
	orderAddField(body, "description", write.Description, owned["description"])
	orderAddField(body, "applicable", write.Applicable, owned["applicable"])
	orderAddField(body, "vatEnabled", write.VATEnabled, owned["vat_enabled"])
	orderAddField(body, "vatIncluded", write.VATIncluded, owned["vat_included"])
	if write.Name.IsKnown() && len(*write.Name.Value) > 255 {
		return orders.ErrInvalid
	}
	if write.Description.IsKnown() && len(*write.Description.Value) > 4096 {
		return orders.ErrInvalid
	}
	orderAddMoment(body, "moment", write.Moment, owned["moment"])
	orderAddMoment(body, "deliveryPlannedMoment", write.DeliveryAt, owned["delivery_at"])
	return nil
}

func orderAddField[T any](body map[string]any, key string, field orders.Field[T], owned bool) {
	if owned && field.IsKnown() {
		body[key] = *field.Value
	}
}

func orderAddMoment(body map[string]any, key string, field orders.Field[time.Time], owned bool) {
	if owned && field.IsKnown() {
		body[key] = field.Value.In(time.FixedZone("Moscow", 3*60*60)).Format("2006-01-02 15:04:05.000")
	}
}

func (c *OrderClient) addOrderCollections(body map[string]any, command orders.Command, owned map[string]bool) error {
	if owned["positions"] {
		if command.Action == orders.ActionMirrorUpdate {
			// The reviewed collection-write contract has no retained position ID.
			// Recreating native lines would lose identity and unmanaged fields.
			return orders.ErrBlocked
		}
		positions, err := c.orderPositions(command.CustomerOrder.Positions)
		if err != nil {
			return err
		}
		body["positions"] = positions
	}
	attributes := c.orderAttributes(command.CustomerOrder.Attributes, owned)
	if len(attributes) > 0 {
		body["attributes"] = attributes
	}
	return nil
}

func (c *OrderClient) orderPositions(positions []orders.PositionWrite) ([]map[string]any, error) {
	if len(positions) > 1000 {
		return nil, orders.ErrBlocked
	}
	result := make([]map[string]any, 0, len(positions))
	for _, position := range positions {
		if !orderUUID.MatchString(position.Mapping.MoyskladID) || !position.Price.IsKnown() {
			return nil, orders.ErrBlocked
		}
		price, err := ordermoney.MoneyToMinor(*position.Price.Value)
		if err != nil {
			return nil, err
		}
		row := map[string]any{"assortment": c.reference("entity/"+position.Mapping.MoyskladKind, position.Mapping.MoyskladKind, position.Mapping.MoyskladID), "quantity": json.Number(position.Quantity), "price": price}
		if err = orderAddPositionValues(row, position); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, nil
}

func orderAddPositionValues(row map[string]any, position orders.PositionWrite) error {
	if position.Reserve.IsKnown() {
		row["reserve"] = json.Number(*position.Reserve.Value)
	}
	if position.VAT.IsKnown() {
		if err := orderVAT(*position.VAT.Value); err != nil {
			return err
		}
		row["vat"] = json.Number(*position.VAT.Value)
	}
	return nil
}

func (c *OrderClient) orderAttributes(attributes []orders.AttributeWrite, owned map[string]bool) []map[string]any {
	result := make([]map[string]any, 0, len(attributes))
	for _, attribute := range attributes {
		if attribute.Value.IsKnown() && (owned["attribute"] || owned["attributes"] || owned["attribute:"+attribute.AttributeID]) {
			result = append(result, map[string]any{"meta": c.reference("entity/customerorder/metadata/attributes", "attributemetadata", attribute.AttributeID).Meta, "value": *attribute.Value.Value})
		}
	}
	return result
}

func (c *OrderClient) validateOrderPayload(command orders.Command, body map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > orderResponseLimit {
		return nil, orders.ErrInvalid
	}
	endpoint := "/entity/customerorder"
	if command.Action == orders.ActionMirrorUpdate {
		endpoint += "/{id}"
	}
	report := c.manifest.Validate(endpoint, "request", raw)
	if !report.Valid || !report.SchemaVerified {
		return nil, ErrOrderSchema
	}
	return body, nil
}
