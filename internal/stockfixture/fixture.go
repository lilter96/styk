// Package stockfixture supplies example identities for isolated tests.
package stockfixture

import "github.com/lilter96/styk/internal/domain"

const (
	Tenant                = "cross-e2e"
	MoyskladAccount       = "2928cf35-d30a-11f1-0a80-1b9000000355"
	OzonAccount           = "1549477"
	SourceProduct         = "e7de650f-d389-11f1-0a80-1ab800275063"
	SourceWarehouse       = "7035238c-d31c-11f1-0a80-071500070d0a"
	OzonProduct           = "2764544165"
	OzonOffer             = "ATEST-897677"
	OzonSKU               = "2889277907"
	OzonWarehouse         = "1020001323428000"
	MaxQuantity     int64 = 7
	// Automatic policies are independently operator-enrolled and bounded. The
	// older manual one-target exception retains its original seven-unit limit.
	MaxAutomaticQuantity int64 = 10
)

// Scope returns an example account pair; it grants no permissions.
func Scope() domain.StockSyncScope {
	return domain.StockSyncScope{Tenant: Tenant, MoyskladAccount: MoyskladAccount, OzonAccount: OzonAccount}
}
