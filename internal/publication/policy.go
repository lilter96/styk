// Package publication defines deployment switches independently of merchant
// ownership and the durable per-account permissions checked by each workflow.
package publication

import (
	"math"
	"os"
	"strconv"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/testpublication"
)

func CatalogEnabled() bool {
	return enabled("STYK_CATALOG_PUBLICATION_ENABLED", "STYK_TEST_ACCOUNT_CATALOG")
}
func OrdersEnabled() bool {
	return enabled("STYK_ORDERS_PUBLICATION_ENABLED", "STYK_TEST_ACCOUNT_ORDERS")
}
func StockEnabled() bool {
	return StockGeneral() || os.Getenv("STYK_TEST_ACCOUNT_PUBLICATION") == "true"
}
func StockGeneral() bool {
	return os.Getenv("STYK_STOCK_PUBLICATION_ENABLED") == "true" && os.Getenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT") == ""
}

func enabled(general, legacy string) bool {
	return os.Getenv(general) == "true" || os.Getenv(legacy) == "true"
}

func Catalog(scope catalog.Scope) bool {
	return scope.Validate() == nil && (os.Getenv("STYK_CATALOG_PUBLICATION_ENABLED") == "true" || legacy("STYK_TEST_ACCOUNT_CATALOG", domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}))
}
func Orders(scope orders.Scope) bool {
	return scope.Validate() == nil && (os.Getenv("STYK_ORDERS_PUBLICATION_ENABLED") == "true" || legacy("STYK_TEST_ACCOUNT_ORDERS", domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}))
}
func Stock(scope domain.StockSyncScope) bool {
	return scope.Valid() && (StockGeneral() || testpublication.Enabled(scope))
}
func legacy(flag string, scope domain.StockSyncScope) bool {
	return os.Getenv(flag) == "true" && scope == testpublication.Scope()
}

func Quantity(scope domain.StockSyncScope, quantity string) bool {
	if !StockGeneral() {
		return testpublication.AutomaticQuantityAllowed(quantity)
	}
	n, err := strconv.ParseInt(quantity, 10, 64)
	return scope.Valid() && err == nil && n >= 0 && n <= math.MaxInt32 && strconv.FormatInt(n, 10) == quantity
}
func Maximum() int64 {
	if StockGeneral() {
		return math.MaxInt32
	}
	return testpublication.MaxAutomaticQuantity
}
func ManualTarget(scope domain.StockSyncScope, product, warehouse, source, sourceWarehouse, quantity string) bool {
	if !StockGeneral() {
		return testpublication.TargetAllowed(scope, product, warehouse, source, sourceWarehouse, quantity)
	}
	return Stock(scope) && product != "" && warehouse != "" && source != "" && sourceWarehouse != "" && Quantity(scope, quantity)
}
