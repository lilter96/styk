// Package doctordemo seeds only a fresh disposable database through existing persistence paths.
package doctordemo

import (
	"context"
	"errors"
	"strings"
	"time"

	"example.com/styk/internal/domain"
	"example.com/styk/internal/postgres"
)

func Run(ctx context.Context, s *postgres.Store) (postgres.MatchScope, error) {
	scope := postgres.MatchScope{Tenant: "doctor-synthetic", MoyskladAccount: "doctor-moy", OzonAccount: "doctor-ozon"}
	return RunScoped(ctx, s, scope)
}

// RunScoped isolates synthetic evidence under a fresh reserved administrative scope.
func RunScoped(ctx context.Context, s *postgres.Store, scope postgres.MatchScope) (postgres.MatchScope, error) {
	if scope.Validate() != nil || !strings.HasPrefix(scope.Tenant, "doctor-synthetic") || !strings.HasPrefix(scope.MoyskladAccount, "doctor-moy") || !strings.HasPrefix(scope.OzonAccount, "doctor-ozon") {
		return scope, errors.New("demo requires reserved synthetic scope")
	}
	var empty bool
	if err := s.Pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 OR account_id IN ($2,$3)) AND NOT EXISTS(SELECT 1 FROM source_imports WHERE account_id IN ($2,$3))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&empty); err != nil {
		return scope, err
	}
	if !empty {
		return scope, errors.New("demo scope already contains evidence; use a fresh disposable database")
	}
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	start := now.Add(-time.Minute)
	stock, reserve, incoming := "10", "2", "0"
	rfbs := false
	for _, c := range []struct{ source, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := s.RegisterStockConnection(ctx, scope.Tenant, c.source, c.account, "synthetic isolated demo", true); err != nil {
			return scope, err
		}
	}
	_, err := s.SaveInventoryImport(ctx, domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: start, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "item", Article: "offer"}, {Kind: "store", ID: "warehouse"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: "item", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming}}}, "doctor-moy-v1")
	if err != nil {
		return scope, err
	}
	_, err = s.SaveOzonImport(ctx, domain.OzonImport{Account: scope.OzonAccount, StartedAt: start, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "product", OfferID: "offer", SKU: "sku"}}, Warehouses: []domain.OzonWarehouse{{ID: "warehouse", IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: "product", OfferID: "offer", SKU: "sku", WarehouseID: "warehouse", Present: 8, Reserved: 2, FreeStock: 6}}}, "doctor-ozon-v1")
	if err != nil {
		return scope, err
	}
	if _, err = s.ConfirmMatch(ctx, scope, "product", "product", "item", "synthetic explicit confirmation"); err != nil {
		return scope, err
	}
	if _, err = s.ConfirmWarehouseMapping(ctx, scope, "warehouse", "warehouse", "synthetic explicit confirmation"); err != nil {
		return scope, err
	}
	_, err = s.SavePostings(ctx, domain.PostingImport{Tenant: scope.Tenant, Account: scope.OzonAccount, Since: start, Until: now, StartedAt: start, FinishedAt: now, Postings: []domain.PostingObservation{{PostingNumber: "posting", OrderID: "order", OrderNumber: "synthetic-order-number", Status: "awaiting_packaging", WarehouseID: "warehouse", ObservedAt: now, ObservedTimeBasis: "detail_received", Lines: []domain.PostingLine{{OfferID: "offer", SKU: "sku", Quantity: 2}}}}})
	if err != nil {
		return scope, err
	}
	wh := "warehouse"
	qty := "2"
	_, err = s.SaveCustomerOrders(ctx, domain.CustomerOrderImport{Tenant: scope.Tenant, Account: scope.MoyskladAccount, Since: start, Until: now, StartedAt: start, FinishedAt: now, Orders: []domain.CustomerOrderObservation{{ID: "customer-order", Account: scope.MoyskladAccount, WarehouseID: &wh, ObservedAt: now, Positions: []domain.CustomerOrderPosition{{ID: "position", AssortmentKind: "product", AssortmentID: "item", Quantity: &qty, Reserve: &reserve, ObservedAt: now}}}}})
	return scope, err
}
