package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

type OrderWarehouseRow struct {
	OzonID    string `json:"ozon_id"`
	OzonName  string `json:"ozon_name"`
	StoreID   string `json:"store_id"`
	StoreName string `json:"store_name"`
	Status    string `json:"status"`
}

type OrderWarehousePage struct {
	Scope      orders.Scope        `json:"scope"`
	Items      []OrderWarehouseRow `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Confirmed  int                 `json:"confirmed"`
	Total      int                 `json:"total"`
}

// OrderWarehouses adapts the shared warehouse read model for order settings.
// Order authorization/configuration remains an independent precondition.
func (s *Store) OrderWarehouses(ctx context.Context, scope orders.Scope, page orders.PageRequest) (OrderWarehousePage, error) {
	result := OrderWarehousePage{Scope: scope, Items: []OrderWarehouseRow{}}
	if scope.Validate() != nil || page.Limit < 1 || page.Limit > 100 || len(page.Cursor) > 256 {
		return result, orders.ErrInvalid
	}
	result.Items = make([]OrderWarehouseRow, 0, page.Limit)
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if _, err := readOrderConfig(ctx, s.Pool, scope); err != nil {
		return result, err
	}
	overview, err := s.WarehouseOverview(ctx, MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}, WarehouseReadRequest{Limit: page.Limit, Cursor: page.Cursor})
	if err != nil {
		return result, ordersError("read order warehouse overview", err)
	}
	result.Total = overview.Counts.Total
	result.Confirmed = overview.Counts.Mapped
	for _, row := range overview.Items {
		status := "unconfirmed"
		if row.State == "mapped" {
			status = "confirmed"
		}
		if row.State == "unmapped" {
			status = "missing"
		}
		result.Items = append(result.Items, OrderWarehouseRow{OzonID: row.ID, OzonName: row.Name, StoreID: row.StoreID, StoreName: row.StoreName, Status: status})
	}
	if overview.HasNext && len(overview.Items) > 0 {
		result.NextCursor = overview.Items[len(overview.Items)-1].ID
	}
	return result, nil
}
