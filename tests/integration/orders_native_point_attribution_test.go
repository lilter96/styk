//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

// Uses the production collector, native adapter, durable catalog import and
// stock attribution. No full-account customerorder traversal is injected.
func TestNativePointAutomaticCollectorAndDurableStockAttribution(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	for index := range 2 {
		f.approve(t, f.proposal(t, orders.ActionDemandCreate, true, fmt.Sprintf("point-attribution-demand-%d", index), ""))
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
		before := nativeDurableStockImport(t, f)
		if guard := nativeAttributionGuard(t, f, before); !guard.Blocked {
			t.Fatal("stock traversal alone fabricated current native evidence")
		}
		collectNativeAttributionPoint(t, f)
		if guard := nativeAttributionGuard(t, f, before); !guard.Blocked {
			t.Fatal("point read after stock traversal was treated as included in its baseline")
		}
		batch := nativeDurableStockImport(t, f)
		guard := nativeAttributionGuard(t, f, batch)
		if index == 0 {
			if !guard.Blocked {
				t.Fatal("partial shipment incorrectly settled full order")
			}
		} else if guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != orderinventory.Shipped {
			t.Fatalf("fresh exact native evidence failed full shipment attribution: %+v", guard)
		}
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM moy_order_batches`, 0)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_point_reads WHERE code=''`, 2)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_point_snapshots`, 2)
	if f.native.writes.Load() != 2 {
		t.Fatal("native evidence collection dispatched a duplicate mutation")
	}
}

func collectNativeAttributionPoint(t *testing.T, f nativeMovementHTTPFixture) {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 0)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := binding.Client.(orderflow.SourceReader)
	if !ok {
		t.Fatal("production source reader unavailable")
	}
	collector, err := orderflow.NewIngestor(f.db, source, orderflow.IngestionOptions{WorkerID: "native-point-attribution-worker", NativeResolver: f.factory, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = collector.CollectStream(f.ctx, f.config.Scope, orders.NativePointStream); err != nil {
		t.Fatal(err)
	}
}
