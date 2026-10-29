package moysklad

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) decodeOrderFields(ctx context.Context, document orderNative, raw map[string]json.RawMessage, correlation orders.Correlation) (orders.TargetObservation, error) {
	write, err := c.decodeOrderReferences(document)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	if err = decodeOrderScalarFields(raw, &write); err != nil {
		return orders.TargetObservation{}, err
	}
	if write.Attributes, err = c.decodeOrderAttributes(raw["attributes"]); err != nil {
		return orders.TargetObservation{}, err
	}
	currencies, err := c.readOrderCurrencies(ctx)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	currency := ""
	for _, row := range currencies {
		if row.ID == write.CurrencyID {
			currency = row.ISOCode
		}
	}
	if currency == "" {
		return orders.TargetObservation{}, ErrOrderIdentity
	}
	sum, err := orderReadMoney(raw, "sum", currency)
	if err != nil || !sum.IsKnown() {
		return orders.TargetObservation{}, ErrOrderSchema
	}
	return orders.TargetObservation{Scope: c.scope, Correlation: correlation, Fields: write, Sum: sum, ObservedAt: time.Now().UTC()}, nil
}

func (c *OrderClient) decodeOrderAttributes(raw json.RawMessage) ([]orders.AttributeWrite, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var attributes []struct {
		catalogReference
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &attributes) != nil {
		return nil, ErrOrderSchema
	}
	result := make([]orders.AttributeWrite, 0, len(attributes))
	seen := make(map[string]bool, len(attributes))
	for _, attribute := range attributes {
		if !strings.HasPrefix(attribute.Name, "styk-order-attribute-") || !orderUUID.MatchString(strings.TrimPrefix(attribute.Name, "styk-order-attribute-")) {
			continue
		}
		if attribute.Type != "string" && attribute.Type != "text" {
			continue
		}
		id, err := c.referenceID(attribute.catalogReference, "entity/customerorder/metadata/attributes", "attributemetadata")
		if err != nil || seen[id] || (attribute.ID != "" && attribute.ID != id) {
			return nil, ErrOrderIdentity
		}
		seen[id] = true
		value, err := orderReadField[string](map[string]json.RawMessage{"value": attribute.Value}, "value")
		if err != nil {
			return nil, err
		}
		result = append(result, orders.AttributeWrite{AttributeID: id, Value: value})
	}
	return result, nil
}

func (c *OrderClient) decodeOrderReferences(document orderNative) (orders.CustomerOrderWrite, error) {
	write := orders.CustomerOrderWrite{}
	refs := []struct {
		ref        catalogReference
		path, kind string
		out        *string
	}{
		{document.Organization, "entity/organization", "organization", &write.OrganizationID},
		{document.Agent, "entity/counterparty", "counterparty", &write.AgentID},
		{document.Store, "entity/store", "store", &write.StoreID},
		{document.State, "entity/customerorder/metadata/states", "state", &write.StateID},
		{document.SalesChannel, "entity/saleschannel", "saleschannel", &write.SalesChannelID},
		{document.Rate.Currency, "entity/currency", "currency", &write.CurrencyID},
	}
	for _, ref := range refs {
		id, err := c.referenceID(ref.ref, ref.path, ref.kind)
		if err != nil {
			return orders.CustomerOrderWrite{}, err
		}
		*ref.out = id
	}
	if document.Updated == "" {
		return orders.CustomerOrderWrite{}, ErrOrderSchema
	}
	write.RateValue = orders.Field[orders.Decimal]{Presence: orders.PresenceMissing}
	if len(document.Rate.Value) > 0 {
		value, err := orderReadDecimal(map[string]json.RawMessage{"value": document.Rate.Value}, "value")
		if err != nil {
			return orders.CustomerOrderWrite{}, err
		}
		write.RateValue = value
	}
	return write, nil
}

func decodeOrderScalarFields(raw map[string]json.RawMessage, write *orders.CustomerOrderWrite) error {
	var err error
	if write.Name, err = orderReadField[string](raw, "name"); err != nil {
		return err
	}
	if write.Description, err = orderReadField[string](raw, "description"); err != nil {
		return err
	}
	if write.Applicable, err = orderReadField[bool](raw, "applicable"); err != nil {
		return err
	}
	if write.VATEnabled, err = orderReadField[bool](raw, "vatEnabled"); err != nil {
		return err
	}
	if write.VATIncluded, err = orderReadField[bool](raw, "vatIncluded"); err != nil {
		return err
	}
	if write.Moment, err = orderReadMoment(raw, "moment"); err != nil {
		return err
	}
	if write.DeliveryAt, err = orderReadMoment(raw, "deliveryPlannedMoment"); err != nil {
		return err
	}
	return nil
}

func orderReadField[T any](raw map[string]json.RawMessage, key string) (orders.Field[T], error) {
	value, present := raw[key]
	if !present {
		return orders.Field[T]{Presence: orders.PresenceMissing}, nil
	}
	if string(value) == "null" {
		return orders.Field[T]{Presence: orders.PresenceNull}, nil
	}
	var result T
	if json.Unmarshal(value, &result) != nil {
		return orders.Field[T]{}, ErrOrderSchema
	}
	return orders.Present(result), nil
}

func orderReadMoment(raw map[string]json.RawMessage, key string) (orders.Field[time.Time], error) {
	field, err := orderReadField[string](raw, key)
	if err != nil {
		return orders.Field[time.Time]{}, err
	}
	if !field.IsKnown() {
		return orders.Field[time.Time]{Presence: field.Presence}, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
		value, parseErr := time.ParseInLocation(layout, *field.Value, time.FixedZone("Moscow", 3*60*60))
		if parseErr == nil {
			return orders.Present(value.UTC()), nil
		}
	}
	return orders.Field[time.Time]{}, ErrOrderSchema
}

func orderReadDecimal(raw map[string]json.RawMessage, key string) (orders.Field[orders.Decimal], error) {
	field, err := orderReadField[json.Number](raw, key)
	if err != nil {
		return orders.Field[orders.Decimal]{}, err
	}
	if !field.IsKnown() {
		return orders.Field[orders.Decimal]{Presence: field.Presence}, nil
	}
	value := orders.Decimal(*field.Value)
	if value.Validate() != nil {
		return orders.Field[orders.Decimal]{}, ErrOrderSchema
	}
	return orders.Present(value), nil
}

func orderReadMoney(raw map[string]json.RawMessage, key, currency string) (orders.Field[orders.Money], error) {
	minor, err := orderReadDecimal(raw, key)
	if err != nil {
		return orders.Field[orders.Money]{}, err
	}
	if !minor.IsKnown() {
		return orders.Field[orders.Money]{Presence: minor.Presence}, nil
	}
	money, err := ordermoney.MoneyFromMinor(json.Number(*minor.Value), currency)
	if err != nil {
		return orders.Field[orders.Money]{}, err
	}
	return orders.Present(money), nil
}

func (c *OrderClient) decodeOrderPosition(raw json.RawMessage, currency string) (orders.TargetPosition, error) {
	var row struct {
		ID         string           `json:"id"`
		AccountID  string           `json:"accountId"`
		Assortment catalogReference `json:"assortment"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil || json.Unmarshal(raw, &fields) != nil || !orderUUID.MatchString(row.ID) || row.AccountID != c.scope.MoyskladAccount {
		return orders.TargetPosition{}, ErrOrderIdentity
	}
	kind := row.Assortment.Meta.Type
	if kind != "product" && kind != "variant" {
		return orders.TargetPosition{}, orders.ErrBlocked
	}
	id, err := c.referenceID(row.Assortment, "entity/"+kind, kind)
	if err != nil {
		return orders.TargetPosition{}, err
	}
	quantity, err := orderReadDecimal(fields, "quantity")
	if err != nil || !quantity.IsKnown() {
		return orders.TargetPosition{}, ErrOrderSchema
	}
	reserve, err := orderReadDecimal(fields, "reserve")
	if err != nil {
		return orders.TargetPosition{}, err
	}
	price, err := orderReadMoney(fields, "price", currency)
	if err != nil || !price.IsKnown() {
		return orders.TargetPosition{}, ErrOrderSchema
	}
	vat, err := orderReadDecimal(fields, "vat")
	if err != nil {
		return orders.TargetPosition{}, err
	}
	return orders.TargetPosition{ID: row.ID, MoyskladKind: kind, MoyskladID: id, Quantity: *quantity.Value, Reserve: reserve, Price: price, VAT: vat}, nil
}
