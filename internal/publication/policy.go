// Package publication validates connected-account scopes and provider limits.
// Ownership and write consent are checked by each workflow, not deployment flags.
package publication

import (
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
	"math"
	"os"
	"strconv"
)

func CatalogEnabled() bool { return true }
func OrdersEnabled() bool  { return true }
func StockEnabled() bool   { return StockGeneral() }

// StockGeneral separates the explicit local simulator from provider dispatch.
func StockGeneral() bool                     { return os.Getenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT") == "" }
func Catalog(scope catalog.Scope) bool       { return scope.Validate() == nil }
func Orders(scope orders.Scope) bool         { return scope.Validate() == nil }
func Stock(scope domain.StockSyncScope) bool { return scope.Valid() && StockGeneral() }
func Quantity(scope domain.StockSyncScope, quantity string) bool {
	n, err := strconv.ParseInt(quantity, 10, 64)
	return scope.Valid() && err == nil && n >= 0 && n <= math.MaxInt32 && strconv.FormatInt(n, 10) == quantity
}
func Maximum() int64 { return math.MaxInt32 }
func ManualTarget(scope domain.StockSyncScope, product, warehouse, source, sourceWarehouse, quantity string) bool {
	return Stock(scope) && product != "" && warehouse != "" && source != "" && sourceWarehouse != "" && Quantity(scope, quantity)
}
