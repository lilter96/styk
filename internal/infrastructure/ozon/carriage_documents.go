package ozon

import (
	"context"
	"slices"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

const carriageDocumentStatusPath = "/v2/posting/fbs/act/check-status"

func (c *OrdersClient) readCarriageDocuments(ctx context.Context, id string) (*orders.CarriageDocuments, error) {
	data, err := c.orderPost(ctx, carriageDocumentStatusPath, map[string]any{"id": jsonCarriageID(id)})
	if err != nil {
		return nil, err
	}
	return decodeCarriageDocuments(data)
}
func decodeCarriageDocuments(data []byte) (*orders.CarriageDocuments, error) {
	if ordercontract.CheckJSON(data) != nil {
		return nil, orders.ErrSchemaDrift
	}
	root := newOrderFields(data)
	if root.raw["result"] == nil || string(root.raw["result"]) == "null" {
		return nil, orders.ErrSchemaDrift
	}
	f := root.object("result")
	result := &orders.CarriageDocuments{Status: orderKnown(f.text("status")), ActType: orderKnown(f.text("act_type")), Added: f.strings("added_to_act"), Removed: f.strings("removed_from_act")}
	partial := f.boolean("is_partial")
	if result.Status == "" || result.ActType == "" || !partial.IsKnown() || !carriageArrayPresent(f, "added_to_act") || !carriageArrayPresent(f, "removed_from_act") || *f.err != nil {
		return nil, orders.ErrSchemaDrift
	}
	result.Partial = *partial.Value
	seen := map[string]bool{}
	for _, id := range append(slices.Clone(result.Added), result.Removed...) {
		if id == "" || seen[id] {
			return nil, orders.ErrSchemaDrift
		}
		seen[id] = true
	}
	slices.Sort(result.Added)
	slices.Sort(result.Removed)
	return result, nil
}
