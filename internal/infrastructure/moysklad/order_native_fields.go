package moysklad

import (
	"context"
	"encoding/json"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) decodeNativeDocumentFields(ctx context.Context, document nativeDocumentDTO, raw map[string]json.RawMessage, correlation orders.NativeDocumentCorrelation) (orders.NativeDocumentObservation, error) {
	observation := orders.NativeDocumentObservation{Scope: c.scope, Correlation: correlation}
	observation.Correlation.DocumentID = document.ID
	refs := []struct {
		ref        catalogReference
		path, kind string
		out        *string
	}{
		{document.Organization, "entity/organization", "organization", &observation.OrganizationID},
		{document.Agent, "entity/counterparty", "counterparty", &observation.AgentID},
		{document.Store, "entity/store", "store", &observation.StoreID},
		{document.Rate.Currency, "entity/currency", "currency", &observation.CurrencyID},
	}
	for _, ref := range refs {
		id, err := c.referenceID(ref.ref, ref.path, ref.kind)
		if err != nil {
			return observation, err
		}
		*ref.out = id
	}
	if document.Updated == "" {
		return observation, ErrOrderSchema
	}
	if err := c.nativeDocumentLinks(document, &observation); err != nil {
		return observation, err
	}
	if err := decodeNativeDocumentScalars(document, raw, &observation); err != nil {
		return observation, err
	}
	return c.nativeDocumentSum(ctx, raw, observation)
}

func (c *OrderClient) nativeDocumentSum(ctx context.Context, raw map[string]json.RawMessage, observation orders.NativeDocumentObservation) (orders.NativeDocumentObservation, error) {
	currencies, err := c.readOrderCurrencies(ctx)
	if err != nil {
		return observation, err
	}
	currency := ""
	for _, row := range currencies {
		if row.ID == observation.CurrencyID {
			currency = row.ISOCode
		}
	}
	if currency == "" {
		return observation, ErrOrderIdentity
	}
	observation.Sum, err = orderReadMoney(raw, "sum", currency)
	if err != nil || !observation.Sum.IsKnown() {
		return observation, ErrOrderSchema
	}
	return observation, nil
}

func (c *OrderClient) nativeDocumentLinks(document nativeDocumentDTO, observation *orders.NativeDocumentObservation) error {
	observation.CustomerOrderID = orders.Field[string]{Presence: orders.PresenceMissing}
	observation.DemandID = orders.Field[string]{Presence: orders.PresenceMissing}
	if document.CustomerOrder.Meta.Href != "" {
		id, err := c.referenceID(document.CustomerOrder, "entity/customerorder", "customerorder")
		if err != nil {
			return err
		}
		observation.CustomerOrderID = orders.Present(id)
	}
	if document.Demand.Meta.Href != "" {
		id, err := c.referenceID(document.Demand, "entity/demand", "demand")
		if err != nil {
			return err
		}
		observation.DemandID = orders.Present(id)
	}
	return nil
}

func decodeNativeDocumentScalars(document nativeDocumentDTO, raw map[string]json.RawMessage, observation *orders.NativeDocumentObservation) error {
	var err error
	if observation.SourceDeleted, err = orderReadDeleted(raw); err != nil {
		return err
	}
	if observation.Applicable, err = orderReadField[bool](raw, "applicable"); err != nil {
		return err
	}
	if observation.VATEnabled, err = orderReadField[bool](raw, "vatEnabled"); err != nil {
		return err
	}
	if observation.VATIncluded, err = orderReadField[bool](raw, "vatIncluded"); err != nil {
		return err
	}
	if observation.Moment, err = orderReadMoment(raw, "moment"); err != nil {
		return err
	}
	observation.RateValue, err = orderReadDecimal(map[string]json.RawMessage{"value": document.Rate.Value}, "value")
	if len(document.Rate.Value) == 0 {
		observation.RateValue = orders.Field[orders.Decimal]{Presence: orders.PresenceMissing}
		return nil
	}
	return err
}
