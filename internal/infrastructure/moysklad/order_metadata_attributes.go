package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

type orderAttributeDefinition struct {
	catalogReference
	Name string `json:"name"`
	Type string `json:"type"`
}

func (c *OrderClient) readOrderAttributeDefinitions(ctx context.Context, raw json.RawMessage) ([]OrderAttribute, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil, ErrOrderSchema
	}
	var rows []json.RawMessage
	if strings.HasPrefix(text, "[") {
		if json.Unmarshal(raw, &rows) != nil || len(rows) > 100 {
			return nil, ErrOrderSchema
		}
	} else {
		empty, err := c.orderAttributeCollectionEmpty(raw)
		if err != nil {
			return nil, err
		}
		if !empty {
			err = c.client.pages(ctx, "entity/customerorder/metadata/attributes", url.Values{}, func(row json.RawMessage) error {
				if len(rows) >= 100 {
					return ErrOrderSchema
				}
				rows = append(rows, row)
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	result := make([]OrderAttribute, 0, len(rows))
	seen := make(map[string]bool)
	for _, rawRow := range rows {
		var attribute orderAttributeDefinition
		if json.Unmarshal(rawRow, &attribute) != nil {
			return nil, ErrOrderSchema
		}
		id, err := c.referenceID(attribute.catalogReference, "entity/customerorder/metadata/attributes", "attributemetadata")
		if err != nil || seen[id] {
			return nil, ErrOrderIdentity
		}
		seen[id] = true
		result = append(result, OrderAttribute{OrderReference: OrderReference{ID: id, Name: attribute.Name}, Type: attribute.Type})
	}
	return result, nil
}
func (c *OrderClient) orderAttributeCollectionEmpty(raw json.RawMessage) (bool, error) {
	var collection struct {
		Meta struct {
			Href   string `json:"href"`
			Size   *int64 `json:"size"`
			Offset *int64 `json:"offset"`
		} `json:"meta"`
	}
	if json.Unmarshal(raw, &collection) != nil {
		return false, ErrOrderSchema
	}
	expected := c.client.base.ResolveReference(&url.URL{Path: "entity/customerorder/metadata/attributes"})
	if collection.Meta.Href != expected.String() {
		return false, ErrOrderIdentity
	}
	if collection.Meta.Size != nil && *collection.Meta.Size < 0 {
		return false, ErrOrderSchema
	}
	if collection.Meta.Offset != nil && *collection.Meta.Offset != 0 {
		return false, ErrOrderSchema
	}
	return collection.Meta.Size != nil && *collection.Meta.Size == 0, nil
}
