//go:build integration

package integration

import (
	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
	"testing"
	"time"
)

func TestOrdersFreshCarrierConflictCannotRestorePreviousAuthorityPostgres(t *testing.T) {
	f := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	cap := orders.CapabilityRef{ID: "previous-carrier-evidence", Revision: 1, WarehouseID: "ozon-warehouse", DeliveryMethodID: "delivery-method", Scheme: "rFBS", Carrier: orders.Present("non_integrated"), Express: orders.Present(false), VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express"}, ObservedAt: now, ValidUntil: now.Add(5 * time.Minute)}
	posting := ordersStorePosting(f.config.Scope, "carrier-conflict", "carrier-before", now)
	posting.Scheme = orders.Present("rFBS")
	posting.DeliveryProfile = &cap
	posting.Delivery.IntegrationType = orders.Present("non_integrated")
	ordersStoreCommit(t, f, posting, "carrier-before")
	posting.Evidence.Revision = "carrier-after"
	posting.Evidence.ProviderUpdatedAt = orders.Present(now.Add(time.Second))
	posting.Delivery.IntegrationType = orders.Present("hybrid")
	posting.DeliveryProfile = nil
	posting.Drift = []orders.Drift{{Path: "$.tpl_integration_type", Code: "conflicting_integration"}}
	ordersStoreCommit(t, f, posting, "carrier-after")
	page, err := f.db.OrderPostings(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(page.Postings) != 1 {
		t.Fatalf("read persisted contradiction: %v", err)
	}
	actual := page.Postings[0]
	if actual.DeliveryProfile != nil || !orders.DeliveryIntegrationConflict(actual) {
		t.Fatal("database projection resurrected older carrier authority")
	}
	plan := orderactions.PlanExecution(actual, cap, orders.ActionPostingShip, time.Now())
	if plan.Allowed || len(plan.Blockers) != 1 || plan.Blockers[0].Code != "conflicting_integration" {
		t.Fatalf("previously enrolled mapping bypassed current contradiction: %+v", plan)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work`, 0)
}
