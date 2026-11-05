package moysklad

import (
	"encoding/json"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func compositionRetainedValues(command orders.Command, identity orders.LineIdentity, row map[string]any) error {
	for _, retained := range command.PositionReplacement.Retained {
		if retained.SourceIdentity.Key() != identity.Key() {
			continue
		}
		native, found := compositionFrozenPosition(command, retained.NativePositionID)
		if !found || !native.Discount.IsKnown() || !native.VATEnabled.IsKnown() {
			return orders.ErrBlocked
		}
		row["id"], row["discount"], row["vatEnabled"] = native.ID, json.Number(*native.Discount.Value), *native.VATEnabled.Value
		return nil
	}
	return nil // A reviewed addition receives no invented provider position ID.
}

func (c *OrderClient) compositionPreservedPosition(command orders.Command, id string) (map[string]any, error) {
	native, found := compositionFrozenPosition(command, id)
	if !found || !native.CompositionSafe.IsKnown() || !*native.CompositionSafe.Value || !native.Shipped.IsKnown() || !orders.ExactDecimalEqual(*native.Shipped.Value, "0") || !native.Price.IsKnown() || !native.Reserve.IsKnown() {
		return nil, orders.ErrBlocked
	}
	position := orders.NativeDocumentPosition{ID: native.ID, ItemKind: native.MoyskladKind, ItemID: native.MoyskladID, Quantity: native.Quantity, Price: *native.Price.Value, Discount: native.Discount, VAT: native.VAT, VATEnabled: native.VATEnabled}
	if position.Validate() != nil || native.Reserve.Validate() != nil {
		return nil, orders.ErrBlocked
	}
	if _, err := ordermoney.RemainingQuantity(native.Quantity, *native.Reserve.Value); err != nil {
		return nil, err
	}
	price, err := ordermoney.MoneyToMinor(position.Price)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": native.ID, "assortment": c.reference("entity/"+native.MoyskladKind, native.MoyskladKind, native.MoyskladID), "quantity": json.Number(native.Quantity), "reserve": json.Number(*native.Reserve.Value), "price": price, "discount": json.Number(*native.Discount.Value), "vat": json.Number(*native.VAT.Value), "vatEnabled": *native.VATEnabled.Value}, nil
}

func compositionFrozenPosition(command orders.Command, id string) (orders.TargetPosition, bool) {
	var position orders.TargetPosition
	found := false
	for _, candidate := range command.TargetPositions {
		if candidate.ID != id {
			continue
		}
		if found {
			return orders.TargetPosition{}, false
		}
		position, found = candidate, true
	}
	return position, found
}
