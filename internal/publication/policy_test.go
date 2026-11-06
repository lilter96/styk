package publication

import (
	"testing"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func TestConnectedScopesAndProtocolQuantityBounds(t *testing.T) {
	for _, key := range []string{"STYK_CATALOG_PUBLICATION_ENABLED", "STYK_ORDERS_PUBLICATION_ENABLED", "STYK_STOCK_PUBLICATION_ENABLED"} {
		t.Setenv(key, "false")
	}
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	for _, tenant := range []string{"merchant-one", "merchant-two"} {
		t.Run(tenant, func(t *testing.T) {
			scope := domain.StockSyncScope{Tenant: tenant, MoyskladAccount: "connected-moy", OzonAccount: "900002"}
			if !Stock(scope) || !Orders(orders.Scope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}) || !Catalog(catalog.Scope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}) {
				t.Fatal("connected scope still restricted to a fixture")
			}
			for _, q := range []string{"0", "11", "500", "2147483647"} {
				if !ManualTarget(scope, "arbitrary-product", "arbitrary-warehouse", "arbitrary-source", "arbitrary-store", q) {
					t.Fatal("valid provider quantity restricted", q)
				}
			}
			for _, q := range []string{"-1", "01", "1.5", "2147483648"} {
				if Quantity(scope, q) {
					t.Fatal("invalid provider quantity accepted", q)
				}
			}
		})
	}
	if Stock(domain.StockSyncScope{}) || Orders(orders.Scope{}) || Catalog(catalog.Scope{}) {
		t.Fatal("invalid scope accepted")
	}
}

func TestSimulatorDoesNotEnableConnectedStockWrites(t *testing.T) {
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	if StockGeneral() || StockEnabled() {
		t.Fatal("simulator flag enabled real writes")
	}
}
