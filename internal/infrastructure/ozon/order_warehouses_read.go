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

// readOrderMethods follows the documented v2 cursor, never the retired v1
// offset. A partially traversed set must not become capability evidence.
func (c *OrdersClient) readOrderMethods(ctx context.Context) ([]delivery.Method, error) {
	result := make([]delivery.Method, 0, 100)
	cursors, seen := make(map[string]bool), make(map[string]bool)
	cursor := ""
	for range 10 {
		data, err := c.orderPost(ctx, orderMethodsPath, map[string]any{"filter": map[string]any{"status": []string{"NEW", "EDITED", "ACTIVE", "DISABLED", "WAITING", "BROKEN"}}, "limit": 100, "cursor": cursor, "sort_dir": "ASC"})
		if err != nil {
			return nil, err
		}
		fields := newOrderFields(data)
		rows, more, next := fields.array("delivery_methods"), fields.boolean("has_next"), fields.text("cursor")
		if rows == nil || !more.IsKnown() || len(rows) > 100 {
			return nil, errOrderPayload
		}
		if err = appendOrderMethods(&result, rows, seen); err != nil || *fields.err != nil {
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

func appendOrderMethods(result *[]delivery.Method, rows []orderFields, seen map[string]bool) error {
	for _, row := range rows {
		method := delivery.Method{ID: orderKnown(row.id("id")), WarehouseID: orderKnown(row.id("warehouse_id")), Express: row.boolean("is_express"), Status: row.text("status")}
		if method.ID == "" || method.WarehouseID == "" || seen[method.ID] || *row.err != nil || len(*result) >= maxDeliveryRows {
			return errOrderPayload
		}
		seen[method.ID] = true
		*result = append(*result, method)
	}
	return nil
}
