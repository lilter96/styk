package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type OrderReference struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type OrderState struct {
	OrderReference
	StateType string `json:"state_type"`
}

type OrderAttribute struct {
	OrderReference
	Type string `json:"type"`
}

type OrderCurrency struct {
	ID      string         `json:"id"`
	ISOCode string         `json:"iso_code"`
	Rate    orders.Decimal `json:"rate"`
	Default bool           `json:"default"`
}

// OrderMetadata contains current-account native references. It never creates a
// counterparty from buyer or receiver PII, or guesses a default counterparty.
type OrderMetadata struct {
	Scope         orders.Scope     `json:"scope"`
	Organizations []OrderReference `json:"organizations"`
	Agents        []OrderReference `json:"agents"`
	Stores        []OrderReference `json:"stores"`
	SalesChannels []OrderReference `json:"sales_channels"`
	States        []OrderState     `json:"states"`
	Attributes    []OrderAttribute `json:"attributes"`
	Currencies    []OrderCurrency  `json:"currencies"`
	ObservedAt    time.Time        `json:"observed_at"`
}

func (c *OrderClient) ReadOrderMetadata(ctx context.Context, scope orders.Scope) (OrderMetadata, error) {
	if !c.scopeAllowed(scope) {
		return OrderMetadata{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return OrderMetadata{}, err
	}
	return c.readOrderMetadata(ctx)
}

func (c *OrderClient) readOrderMetadata(ctx context.Context) (OrderMetadata, error) {
	result := OrderMetadata{Scope: c.scope, ObservedAt: time.Now().UTC()}
	if err := c.readOrderDefinitions(ctx, &result); err != nil {
		return OrderMetadata{}, err
	}
	refs := []struct {
		kind string
		out  *[]OrderReference
	}{
		{"organization", &result.Organizations}, {"counterparty", &result.Agents},
		{"store", &result.Stores}, {"saleschannel", &result.SalesChannels},
	}
	for _, ref := range refs {
		rows, err := c.readOrderReferences(ctx, ref.kind)
		if err != nil {
			return OrderMetadata{}, err
		}
		*ref.out = rows
	}
	currencies, err := c.readOrderCurrencies(ctx)
	if err != nil {
		return OrderMetadata{}, err
	}
	result.Currencies = currencies
	return result, nil
}

func (c *OrderClient) readOrderReferences(ctx context.Context, kind string) ([]OrderReference, error) {
	result := make([]OrderReference, 0)
	seen := make(map[string]bool)
	err := c.client.pages(ctx, "entity/"+kind, url.Values{"order": {"id,asc"}}, func(raw json.RawMessage) error {
		var row struct {
			catalogReference
			ID        string `json:"id"`
			AccountID string `json:"accountId"`
			Name      string `json:"name"`
			Archived  bool   `json:"archived"`
		}
		if json.Unmarshal(raw, &row) != nil || row.AccountID != c.scope.MoyskladAccount || seen[row.ID] {
			return ErrOrderIdentity
		}
		id, err := c.referenceID(row.catalogReference, "entity/"+kind, kind)
		if err != nil || id != row.ID {
			return ErrOrderIdentity
		}
		seen[id] = true
		if !row.Archived {
			result = append(result, OrderReference{ID: id, Name: row.Name})
		}
		return nil
	})
	return result, err
}

func (c *OrderClient) readOrderDefinitions(ctx context.Context, result *OrderMetadata) error {
	raw, err := c.client.get(ctx, "entity/customerorder/metadata", nil)
	if err != nil {
		return err
	}
	var definitions struct {
		States []struct {
			catalogReference
			ID         string `json:"id"`
			AccountID  string `json:"accountId"`
			Name       string `json:"name"`
			StateType  string `json:"stateType"`
			EntityType string `json:"entityType"`
		} `json:"states"`
		Attributes []struct {
			catalogReference
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"attributes"`
	}
	if json.Unmarshal(raw, &definitions) != nil {
		return ErrOrderSchema
	}
	for _, state := range definitions.States {
		id, refErr := c.referenceID(state.catalogReference, "entity/customerorder/metadata/states", "state")
		if refErr != nil || id != state.ID || state.AccountID != c.scope.MoyskladAccount || state.EntityType != "customerorder" {
			return ErrOrderIdentity
		}
		result.States = append(result.States, OrderState{OrderReference: OrderReference{ID: id, Name: state.Name}, StateType: state.StateType})
	}
	for _, attribute := range definitions.Attributes {
		id, refErr := c.referenceID(attribute.catalogReference, "entity/customerorder/metadata/attributes", "attributemetadata")
		if refErr != nil {
			return refErr
		}
		result.Attributes = append(result.Attributes, OrderAttribute{OrderReference: OrderReference{ID: id, Name: attribute.Name}, Type: attribute.Type})
	}
	return nil
}

func (c *OrderClient) readOrderCurrencies(ctx context.Context) ([]OrderCurrency, error) {
	result := make([]OrderCurrency, 0)
	seen := make(map[string]bool)
	err := c.client.pages(ctx, "entity/currency", nil, func(raw json.RawMessage) error {
		var row struct {
			catalogReference
			ID       string      `json:"id"`
			ISOCode  string      `json:"isoCode"`
			Rate     json.Number `json:"rate"`
			Default  bool        `json:"default"`
			Archived bool        `json:"archived"`
		}
		if json.Unmarshal(raw, &row) != nil || seen[row.ID] || (orders.Money{Amount: "0", Currency: row.ISOCode}).Validate() != nil || orders.Decimal(row.Rate).Validate() != nil {
			return ErrOrderSchema
		}
		id, refErr := c.referenceID(row.catalogReference, "entity/currency", "currency")
		if refErr != nil || id != row.ID {
			return ErrOrderIdentity
		}
		seen[id] = true
		if !row.Archived {
			result = append(result, OrderCurrency{ID: id, ISOCode: row.ISOCode, Rate: orders.Decimal(row.Rate), Default: row.Default})
		}
		return nil
	})
	return result, err
}
