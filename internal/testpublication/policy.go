// Package testpublication defines the owner's single, explicitly authorized
// live test target. The deployment switch cannot authorize arbitrary accounts.
package testpublication

import (
	"os"
	"strconv"

	"github.com/lilter96/styk/internal/domain"
)

const (
	Tenant                = "cross-e2e"
	MoyskladAccount       = "2928cf35-d30a-11f1-0a80-1b9000000355"
	OzonAccount           = "1549477"
	SourceProduct         = "e7de650f-d389-11f1-0a80-1ab800275063"
	SourceWarehouse       = "7035238c-d31c-11f1-0a80-071500070d0a"
	OzonProduct           = "2764544165"
	OzonOffer             = "ATEST-897677"
	OzonWarehouse         = "1020001323428000"
	MaxQuantity     int64 = 7
)

// Scope is the only live account pair this test policy can ever authorize.
func Scope() domain.StockSyncScope {
	return domain.StockSyncScope{Tenant: Tenant, MoyskladAccount: MoyskladAccount, OzonAccount: OzonAccount}
}

// Enabled is default-off, and the switch cannot widen its fixed account scope.
func Enabled(scope domain.StockSyncScope) bool {
	return os.Getenv("STYK_TEST_ACCOUNT_PUBLICATION") == "true" && scope == Scope()
}

func TargetAllowed(scope domain.StockSyncScope, product, warehouse, source, sourceWarehouse, quantity string) bool {
	if !Enabled(scope) || product != OzonProduct || warehouse != OzonWarehouse || source != SourceProduct || sourceWarehouse != SourceWarehouse {
		return false
	}
	return QuantityAllowed(quantity)
}

func QuantityAllowed(quantity string) bool {
	n, err := strconv.ParseInt(quantity, 10, 64)
	return err == nil && n >= 0 && n <= MaxQuantity && strconv.FormatInt(n, 10) == quantity
}
