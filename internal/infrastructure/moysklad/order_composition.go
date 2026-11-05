package moysklad

import (
	"context"
	"encoding/json"

	"github.com/lilter96/styk/internal/orders"
)

// Official customerOrder contract (retrieved 2026-11-04) documents full parent
// positions replacement and retained position IDs. We send every supported
// writable line value, never roundtrip raw provider JSON or rely on omitted
// nested properties merging. Pack/unknown extensions block this narrow path.
func (c *OrderClient) compositionPayload(command orders.Command) (map[string]any, error) {
	if command.PositionReplacement == nil || command.PositionReplacement.Validate(command) != nil {
		return nil, orders.ErrBlocked
	}
	rows, err := c.orderPositions(command.CustomerOrder.Positions)
	if err != nil {
		return nil, err
	}
	for i, wanted := range command.CustomerOrder.Positions {
		row := rows[i]
		row["discount"] = json.Number("0")
		row["vatEnabled"] = *command.PositionReplacement.VATEnabled.Value
		if err = compositionRetainedValues(command, wanted.Identity, row); err != nil {
			return nil, err
		}
	}
	for _, id := range command.PositionReplacement.Preserved {
		row, preserveErr := c.compositionPreservedPosition(command, id)
		if preserveErr != nil {
			return nil, preserveErr
		}
		rows = append(rows, row)
	}
	return c.validateOrderPayload(command, map[string]any{"positions": rows})
}

func (c *OrderClient) compositionPreflight(ctx context.Context, command orders.Command) error {
	if command.Action != orders.ActionMirrorCompositionUpdate {
		return nil
	}
	target, err := c.readCustomerOrder(ctx, command.Correlation)
	if err != nil {
		return err
	}
	return orders.CompositionTargetCurrent(command, target)
}

func orderCompositionPositionSafe(fields map[string]json.RawMessage) bool {
	allowed := map[string]bool{"meta": true, "id": true, "accountId": true, "quantity": true, "reserve": true, "shipped": true, "stock": true, "inTransit": true, "price": true, "vat": true, "vatEnabled": true, "discount": true, "assortment": true}
	for name, value := range fields {
		if !allowed[name] && (name != "pack" || string(value) != "null") {
			return false
		}
	}
	return true
}
