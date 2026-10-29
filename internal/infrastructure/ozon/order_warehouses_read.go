package ozon

import (
	"context"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/orders"
)

func (c *OrdersClient) readOrderWarehouses(ctx context.Context) ([]delivery.Warehouse, error) {
	result := make([]delivery.Warehouse, 0, 100)
	cursors, seen := make(map[string]bool), make(map[string]bool)
	cursor := ""
	for range 10 {
		data, err := c.orderPost(ctx, orderWarehousePath, map[string]any{"limit": 100, "cursor": cursor})
		if err != nil {
			return nil, err
		}
		fields := newOrderFields(data)
		rows, more, next := fields.array("warehouses"), fields.boolean("has_next"), fields.text("cursor")
		if err = appendOrderWarehouses(&result, rows, seen); err != nil || *fields.err != nil || rows == nil || !more.IsKnown() {
			return nil, errOrderPayload
		}
		if !*more.Value {
			return result, nil
		}
		if !next.IsKnown() || *next.Value == cursor || cursors[*next.Value] || len(rows) == 0 {
			return nil, errOrderPayload
		}
		cursor, cursors[*next.Value] = *next.Value, true
	}
	return nil, orders.ErrBlocked
}

func appendOrderWarehouses(result *[]delivery.Warehouse, rows []orderFields, seen map[string]bool) error {
	for _, row := range rows {
		warehouse := delivery.Warehouse{ID: orderKnown(row.id("warehouse_id")), Type: row.text("warehouse_type"), Status: row.text("status"), IsRFBS: row.boolean("is_rfbs"), Express: row.boolean("is_express")}
		if warehouse.ID == "" || seen[warehouse.ID] || len(*result) >= maxDeliveryRows {
			return errOrderPayload
		}
		seen[warehouse.ID] = true
		*result = append(*result, warehouse)
	}
	return nil
}

func (c *OrdersClient) readOrderMethods(ctx context.Context) ([]delivery.Method, error) {
	// The observed current v2 request uses offset, whereas its response includes
	// cursor. Continuation semantics remain unverified; never guess a cursor or
	// offset increment from those unrelated fields.
	data, err := c.orderPost(ctx, orderMethodsPath, map[string]any{"filter": struct{}{}, "limit": 50, "offset": 0})
	if err != nil {
		return nil, err
	}
	fields := newOrderFields(data)
	rows, more := fields.array("delivery_methods"), fields.boolean("has_next")
	if *fields.err != nil || rows == nil || !more.IsKnown() || len(rows) > 50 {
		return nil, errOrderPayload
	}
	if *more.Value {
		return nil, orders.ErrBlocked
	}
	result := make([]delivery.Method, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		method := delivery.Method{ID: orderKnown(row.id("id")), WarehouseID: orderKnown(row.id("warehouse_id")), Express: row.boolean("is_express")}
		if method.ID == "" || method.WarehouseID == "" || seen[method.ID] || *fields.err != nil {
			return nil, errOrderPayload
		}
		seen[method.ID] = true
		result = append(result, method)
	}
	return result, nil
}
