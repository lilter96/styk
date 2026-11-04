package moysklad

import (
	"encoding/json"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

// Contract checked 2026-11-04: https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/documents/_customerOrder.md
// Source SHA-256: 990e50b153bd64ad0b423710f88fc435564878aa95485b792759415f0563c972.
// The official customerorder position PUT accepts only changed fields. It does
// not replace the collection or require resending assortment, discounts or tax.
func (c *OrderClient) orderPositionPayload(command orders.Command) (map[string]any, error) {
	patch := command.PositionPatch
	if patch == nil || !orderUUID.MatchString(patch.PositionID) || !orderUUID.MatchString(command.Correlation.CustomerOrderID) {
		return nil, ErrOrderIdentity
	}
	body := make(map[string]any, 3)
	if patch.Quantity.IsKnown() {
		body["quantity"] = json.Number(*patch.Quantity.Value)
	}
	if patch.Reserve.IsKnown() {
		body["reserve"] = json.Number(*patch.Reserve.Value)
	}
	if patch.Price.IsKnown() {
		price, err := ordermoney.MoneyToMinor(*patch.Price.Value)
		if err != nil {
			return nil, err
		}
		body["price"] = price
	}
	if len(body) == 0 {
		return nil, orders.ErrBlocked
	}
	return body, nil
}

func (c *OrderClient) orderPositionResponse(command orders.Command, raw []byte, status int) (orders.Receipt, error) {
	patch := command.PositionPatch
	if patch == nil {
		return orderReceipt(command, orders.StateUnknown, status, "position_acknowledgement_unverified"), ErrOrderUnknown
	}
	var reference catalogReference
	path := "entity/customerorder/" + command.Correlation.CustomerOrderID + "/positions"
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &reference) != nil {
		return orderReceipt(command, orders.StateUnknown, status, "position_acknowledgement_unverified"), ErrOrderUnknown
	}
	id, err := c.referenceID(reference, path, "customerorderposition")
	if err != nil || id != patch.PositionID {
		return orderReceipt(command, orders.StateUnknown, status, "position_acknowledgement_unverified"), ErrOrderUnknown
	}
	currency := ""
	for _, binding := range patch.Baseline.Bindings {
		if binding.Native.ID == patch.PositionID && binding.Native.Price.IsKnown() {
			currency = binding.Native.Price.Value.Currency
		}
	}
	position, err := c.decodeOrderPosition(raw, currency)
	if err != nil || position.ID != id {
		return orderReceipt(command, orders.StateUnknown, status, "position_acknowledgement_unverified"), ErrOrderUnknown
	}
	receipt := orderReceipt(command, orders.StateAcknowledged, status, "position_acknowledged")
	receipt.ExternalID = command.Correlation.CustomerOrderID
	return receipt, nil
}
