//go:build integration

package integration

import (
	"context"
	"errors"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
	"testing"
	"time"
)

func TestStockOrderObservationsRemainScopedAndDoNotInventReserve(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx := context.Background()
	scope := testpublication.Scope()
	now := time.Now().UTC()
	qty, zero := "3", "0"
	wh := testpublication.SourceWarehouse
	state := "state"
	yes := true
	orders := domain.CustomerOrderImport{Tenant: scope.Tenant, Account: scope.MoyskladAccount, Since: now.Add(-time.Hour), Until: now, StartedAt: now, FinishedAt: now, Orders: []domain.CustomerOrderObservation{{ID: "order", Account: scope.MoyskladAccount, WarehouseID: &wh, StateID: &state, Applicable: &yes, ObservedAt: now, Positions: []domain.CustomerOrderPosition{{ID: "position", AssortmentKind: "product", AssortmentID: testpublication.SourceProduct, Quantity: &qty, Reserve: nil, Shipped: &zero, ObservedAt: now}}}}}
	sourceID, err := db.SaveCustomerOrders(ctx, orders)
	if err != nil {
		t.Fatal(err)
	}
	postings := domain.PostingImport{Tenant: scope.Tenant, Account: scope.OzonAccount, Since: now.Add(-time.Hour), Until: now, StartedAt: now, FinishedAt: now, Postings: []domain.PostingObservation{{PostingNumber: "posting-1", OrderID: "order-1", OrderNumber: "number-1", Status: "awaiting_packaging", WarehouseID: testpublication.OzonWarehouse, ObservedAt: now, ObservedTimeBasis: "detail_received", Lines: []domain.PostingLine{{OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Quantity: 3}}}}}
	postID, err := db.SavePostings(ctx, postings)
	if err != nil {
		t.Fatal(err)
	}
	e, err := db.StockOrderEvidence(ctx, scope, testpublication.OzonProduct, testpublication.OzonWarehouse)
	if err != nil || e.BatchID == nil || *e.BatchID != sourceID || e.PostingBatchID == nil || *e.PostingBatchID != postID || len(e.Positions) != 1 || e.Positions[0].Reserve != nil || e.Positions[0].Quantity == nil || *e.Positions[0].Quantity != "3" || len(e.Postings) != 1 || e.Postings[0].Quantity != 3 || e.WindowFrom == nil || e.PostingWindowFrom == nil {
		t.Fatalf("evidence changed or missing: %+v %v", e, err)
	}
	wrong := scope
	wrong.Tenant = "other"
	if _, err = db.StockOrderEvidence(ctx, wrong, testpublication.OzonProduct, testpublication.OzonWarehouse); !errors.Is(err, domain.ErrStockSyncNotFound) {
		t.Fatalf("other tenant exposed: %v", err)
	}
	wrong = scope
	wrong.OzonAccount = "other"
	if _, err = db.StockOrderEvidence(ctx, wrong, testpublication.OzonProduct, testpublication.OzonWarehouse); !errors.Is(err, domain.ErrStockSyncNotFound) {
		t.Fatalf("other account exposed: %v", err)
	}
	if _, err = db.StockOrderEvidence(ctx, scope, testpublication.OzonProduct, "1"); !errors.Is(err, domain.ErrStockSyncNotFound) {
		t.Fatalf("wrong warehouse exposed: %v", err)
	}
}
