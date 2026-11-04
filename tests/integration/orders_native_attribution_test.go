//go:build integration

package integration

import (
	"fmt"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/providercall"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func nativeDurableStockImport(t *testing.T, f nativeMovementHTTPFixture) int64 {
	t.Helper()
	key := fmt.Sprintf("native-attribution-stock-%d", f.provider.ingests.Add(1))
	op, _, err := f.db.SubmitCatalogImport(f.ctx, f.config.Scope.Tenant, "moysklad", f.config.Scope.MoyskladAccount, key, f.session.AuditID, domain.DefaultMoyskladImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.db.ClaimForCatalogWorker(f.ctx, 10*time.Second, "", f.config.Scope.MoyskladAccount)
	if err != nil || task == nil {
		t.Fatal("normal durable import claim", err)
	}
	client, err := f.factory.NewMoysklad(f.config.Scope.MoyskladAccount, "synthetic-orders-moysklad-"+f.config.Scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Coordinate(f.factory.Gate, providercall.Scope{Tenant: f.config.Scope.Tenant, Provider: "moysklad", Account: f.config.Scope.MoyskladAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = (ozonjob.Runner{Store: f.db, MoyskladAccount: f.config.Scope.MoyskladAccount, MoyskladClient: client}).ProcessImport(f.ctx, *task, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	completed, err := f.db.GetCatalogOperation(f.ctx, f.config.Scope.Tenant, "moysklad", f.config.Scope.MoyskladAccount, op.ID)
	if err != nil || completed.BatchID == nil || completed.State != "SUCCEEDED" {
		t.Fatal("normal durable import not complete", err)
	}
	return *completed.BatchID
}

func nativeActualOrderTraversal(t *testing.T, f nativeMovementHTTPFixture) {
	t.Helper()
	handler := f.provider.base.server.Config.Handler
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/entity/customerorder" || !strings.HasPrefix(r.URL.Query().Get("filter"), "updated>=") {
			handler.ServeHTTP(w, r)
			return
		}
		base := f.provider.base
		account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
		base.mu.Lock()
		defer base.mu.Unlock()
		rows := make([]any, 0, len(base.native))
		for _, native := range base.native {
			if native.account == account && !native.hidden {
				rows = append(rows, base.document(native))
			}
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			base.t.Error(err)
			w.WriteHeader(500)
			return
		}
		base.page(w, rows, len(rows), limit, 0)
	})
	client, err := f.factory.NewMoysklad(f.config.Scope.MoyskladAccount, "synthetic-orders-moysklad-"+f.config.Scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Coordinate(f.factory.Gate, providercall.Scope{Tenant: f.config.Scope.Tenant, Provider: "moysklad", Account: f.config.Scope.MoyskladAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Truncate(time.Millisecond)
	evidence, err := client.ReadCustomerOrders(f.ctx, f.config.Scope.Tenant, f.config.Scope.MoyskladAccount, until.Add(-7*24*time.Hour), until)
	if err != nil || len(evidence.Orders) != 1 {
		t.Fatal("actual complete native order traversal", err)
	}
	if _, err = f.db.SaveCustomerOrders(f.ctx, evidence); err != nil {
		t.Fatal(err)
	}
}

func nativeAttributionGuard(t *testing.T, f nativeMovementHTTPFixture, batch int64) orderinventory.Guard {
	t.Helper()
	request := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: f.config.Scope.Tenant, MoyskladAccount: f.config.Scope.MoyskladAccount, WarehouseID: f.config.StoreID, ItemKind: "product", ItemID: ordersProcessID(8)}, Target: domain.StockTarget{OzonAccount: f.config.Scope.OzonAccount, ProductID: "1001", WarehouseID: "3001"}, Policy: domain.StockCalculationPolicy{Version: 1, MaxObservationAge: time.Hour, DecisionLifetime: time.Minute}}
	guard, err := f.db.OrderInventoryGuard(f.ctx, request, batch, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func TestNativeAttributionActualHTTPAndPostgresFullShipmentSettlesOnlyAfterFreshReads(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	for index := range 2 {
		preview := f.proposal(t, orders.ActionDemandCreate, true, fmt.Sprintf("actual-attribution-demand-%d", index), "")
		f.approve(t, preview)
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
		oldOrderBatch := nativeDurableStockImport(t, f)
		if guard := nativeAttributionGuard(t, f, oldOrderBatch); !guard.Blocked {
			t.Fatal("fresh stock with missing/stale native order traversal settled obligation")
		}
		nativeActualOrderTraversal(t, f)
		batch := nativeDurableStockImport(t, f)
		guard := nativeAttributionGuard(t, f, batch)
		if index == 0 {
			if !guard.Blocked {
				t.Fatal("partial shipment settled whole posting")
			}
			continue
		}
		if guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != orderinventory.Shipped {
			t.Fatalf("full native shipment not attributed %+v", guard)
		}
		review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{"position-http-posting"}, false)
		if err != nil {
			t.Fatal(err)
		}
		baseline := review.PositionBaselines["position-http-posting"]
		if baseline.EvidenceClass != orders.NativeDemandReadback || len(baseline.Bindings) != 1 || !baseline.Bindings[0].Native.Shipped.IsKnown() || *baseline.Bindings[0].Native.Shipped.Value != "2" {
			t.Fatalf("native position frontier unavailable %+v", baseline)
		}
		var originalFinished, afterFinished time.Time
		if err = f.db.Pool.QueryRow(f.ctx, `SELECT finished_at FROM source_imports WHERE id=$1`, batch).Scan(&originalFinished); err != nil {
			t.Fatal(err)
		}
		repeated := nativeDurableStockImport(t, f)
		if repeated != batch {
			t.Fatal("unchanged provider content created another source snapshot")
		}
		if err = f.db.Pool.QueryRow(f.ctx, `SELECT finished_at FROM source_imports WHERE id=$1`, batch).Scan(&afterFinished); err != nil || !afterFinished.Equal(originalFinished) {
			t.Fatal("fresh traversal overwrote original snapshot timestamp", err)
		}
		if guard := nativeAttributionGuard(t, f, repeated); guard.Blocked {
			t.Fatalf("fresh unchanged content lost native attribution %+v", guard)
		}

	}
	if f.native.writes.Load() != 2 {
		t.Fatal("native facts learned with duplicate dispatch")
	}
}
