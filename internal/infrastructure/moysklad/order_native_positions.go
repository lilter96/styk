package moysklad

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) readNativePositions(ctx context.Context, document nativeDocumentDTO, kind, currency string) ([]orderPositionRead, []orders.NativeDocumentPosition, error) {
	path := "entity/" + kind + "/" + document.ID + "/positions"
	if document.Positions.Meta.Href != c.client.base.ResolveReference(&url.URL{Path: path}).String() || document.Positions.Meta.Type != kind+"position" || document.Positions.Meta.Size == nil || *document.Positions.Meta.Size < 1 || *document.Positions.Meta.Size > 1000 {
		return nil, nil, ErrOrderSchema
	}
	rows := make([]orderPositionRead, 0, *document.Positions.Meta.Size)
	positions := make([]orders.NativeDocumentPosition, 0, *document.Positions.Meta.Size)
	seen := make(map[string]bool, *document.Positions.Meta.Size)
	err := c.client.pages(ctx, path, nil, func(raw json.RawMessage) error {
		target, decodeErr := c.decodeOrderPosition(raw, currency)
		if decodeErr != nil {
			return decodeErr
		}
		if seen[target.ID] {
			return orders.ErrConflict
		}
		seen[target.ID] = true
		position, decodeErr := decodeNativePosition(target, raw)
		if decodeErr != nil {
			return decodeErr
		}
		positions = append(positions, position)
		rows = append(rows, orderPositionRead{Target: target, Raw: raw})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(rows) != *document.Positions.Meta.Size {
		return nil, nil, orders.ErrStale
	}
	return rows, positions, nil
}

func decodeNativePosition(target orders.TargetPosition, raw json.RawMessage) (orders.NativeDocumentPosition, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return orders.NativeDocumentPosition{}, ErrOrderSchema
	}
	vatEnabled, decodeErr := orderReadField[bool](fields, "vatEnabled")
	if decodeErr != nil {
		return orders.NativeDocumentPosition{}, decodeErr
	}
	position := orders.NativeDocumentPosition{ID: target.ID, ItemKind: target.MoyskladKind, ItemID: target.MoyskladID, Quantity: target.Quantity, Price: *target.Price.Value, Discount: target.Discount, VAT: target.VAT, VATEnabled: vatEnabled}
	if decodeErr = position.Validate(); decodeErr != nil {
		return orders.NativeDocumentPosition{}, decodeErr
	}
	return position, nil
}
