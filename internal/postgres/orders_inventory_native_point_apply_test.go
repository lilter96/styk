package postgres

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

func TestApplyNativePointPreservesZeroMissingAndDeletion(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	point := orders.NativeOrderPoint{Correlation: orders.Correlation{CustomerOrderID: "native", SyncID: "sync"}, StoreID: "store", Applicable: orders.Present(false), StartedAt: at, FinishedAt: at, ObservedAt: at, SourceDeleted: orders.Present("2026-11-04 10:11:12.123"), Positions: []orders.TargetPosition{
		{ID: "owned", MoyskladKind: "product", MoyskladID: "item", Quantity: "2", Reserve: orders.Present(orders.Decimal("0")), Shipped: orders.Field[orders.Decimal]{Presence: orders.PresenceNull}},
		{ID: "foreign", MoyskladKind: "product", MoyskladID: "other", Quantity: "3"},
	}}
	var observation orderinventory.Observation
	applyInventoryNativePoint(domain.StockPool{ItemKind: "product", ItemID: "item"}, &observation, nativePointInventoryRead{Point: &point, AdmittedAt: at, RecordedAt: at.Add(time.Second)})
	if len(observation.Positions) != 1 || observation.NativeSyncID == nil || *observation.NativeSyncID != "sync" {
		t.Fatal("point identity or product filter lost")
	}
	p := observation.Positions[0]
	if p.Reserve == nil || *p.Reserve != "0" || p.Shipped != nil || p.Applicable == nil || *p.Applicable || p.SourceDeleted == nil || *p.SourceDeleted != *point.SourceDeleted.Value {
		t.Fatal("zero/null/false/deletion evidence was coerced")
	}
	if point.Positions[0].Shipped.Presence != orders.PresenceNull {
		t.Fatal("native snapshot was mutated")
	}
}

func TestFailedNativePointClearsCachedNativeProof(t *testing.T) {
	t.Parallel()
	syncID := "cached"
	observation := orderinventory.Observation{NativeSyncID: &syncID, Positions: []domain.StockObservedOrderPosition{{PositionID: "cached"}}}
	applyInventoryNativePoint(domain.StockPool{}, &observation, nativePointInventoryRead{Code: "NATIVE_ORDER_NOT_FOUND"})
	if observation.NativeReadCode != "NATIVE_ORDER_NOT_FOUND" || observation.NativeSyncID != nil || len(observation.Positions) != 0 {
		t.Fatal("failed point silently retained cached native proof")
	}
}
