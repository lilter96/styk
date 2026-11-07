package moysklad

import (
	"context"
	"encoding/json"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) readCommissionPositions(ctx context.Context, doc commissionDocumentDTO, currency string) ([]orders.CommissionObservedLine, error) {
	return c.readCommissionPositionCollection(ctx, doc.ID, "positions", *doc.Positions.Meta.Size, currency)
}

func (c *OrderClient) readCommissionPositionCollection(ctx context.Context, id, suffix string, size int, currency string) ([]orders.CommissionObservedLine, error) {
	lines := make([]orders.CommissionObservedLine, 0, size)
	seen := make(map[string]bool, size)
	for offset := 0; offset < size; {
		raw, err := c.client.get(ctx, "entity/commissionreportin/"+id+"/"+suffix, commissionOffset(offset))
		if err != nil {
			return nil, err
		}
		page, err := commissionCollection(raw, 100, offset)
		if err != nil || *page.Meta.Size != size {
			return nil, ErrOrderSchema
		}
		for _, row := range *page.Rows {
			line, decodeErr := c.commissionObservedPosition(row, currency)
			if decodeErr != nil {
				return nil, decodeErr
			}
			if seen[line.ID] {
				return nil, ErrOrderIdentity
			}
			seen[line.ID] = true
			lines = append(lines, line)
		}
		offset += len(*page.Rows)
	}
	return lines, nil
}

func (c *OrderClient) commissionObservedPosition(raw []byte, currency string) (orders.CommissionObservedLine, error) {
	var dto struct {
		ID         string           `json:"id"`
		AccountID  string           `json:"accountId"`
		Assortment catalogReference `json:"assortment"`
		Quantity   json.Number      `json:"quantity"`
		VAT        json.Number      `json:"vat"`
		VATEnabled *bool            `json:"vatEnabled"`
	}
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &dto) != nil || json.Unmarshal(raw, &fields) != nil || !orderUUID.MatchString(dto.ID) || dto.AccountID != c.scope.MoyskladAccount || dto.VATEnabled == nil {
		return orders.CommissionObservedLine{}, ErrOrderSchema
	}
	result := orders.CommissionObservedLine{ID: dto.ID, ItemKind: dto.Assortment.Meta.Type, Quantity: orders.Decimal(dto.Quantity), VAT: orders.Decimal(dto.VAT), VATEnabled: *dto.VATEnabled}
	if result.ItemKind != "product" && result.ItemKind != "variant" || result.Quantity.Validate() != nil || result.VAT.Validate() != nil {
		return result, ErrOrderSchema
	}
	var err error
	result.ItemID, err = c.referenceID(dto.Assortment, "entity/"+result.ItemKind, result.ItemKind)
	if err != nil {
		return result, err
	}
	result.Price, err = commissionReadMoney(fields, "price", currency)
	if err != nil {
		return result, err
	}
	result.Reward, err = commissionReadMoney(fields, "reward", currency)
	return result, err
}
