package orderinventory

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func nativeEffectFixture() (domain.StockPool, domain.StockSourceEvidence, orders.NativeInventoryEffect, time.Time) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	id := int64(7)
	pool := domain.StockPool{Tenant: "merchant", MoyskladAccount: "moy", WarehouseID: "store", ItemKind: "product", ItemID: "item"}
	source := domain.StockSourceEvidence{Tenant: pool.Tenant, Account: pool.MoyskladAccount, WarehouseID: pool.WarehouseID, ItemKind: pool.ItemKind, ItemID: pool.ItemID, ImportID: &id, ObservationFound: true, TraversalComplete: true}
	effect := orders.NativeInventoryEffect{Scope: orders.Scope{Tenant: pool.Tenant, MoyskladAccount: pool.MoyskladAccount, OzonAccount: "ozon"}, PostingNumber: "posting", WorkID: "work", Kind: "demand", State: orders.StateUnknown, Applicable: true, StoreID: pool.WarehouseID, ItemKind: pool.ItemKind, ItemID: pool.ItemID, ReconciledAt: now.Add(-time.Minute)}
	return pool, source, effect, now
}

func TestNativeEffectPendingFencesOnlyAffectedPhysicalPool(t *testing.T) {
	t.Parallel()
	pool, source, effect, now := nativeEffectFixture()
	for _, state := range []orders.State{orders.StateQueued, orders.StateClaimed, orders.StateDispatching, orders.StateAcknowledged, orders.StatePolling, orders.StateUnknown} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			e := effect
			e.State = state
			g := (Guard{}).WithNativeEffects(pool, source, now.Add(-time.Hour), now, []orders.NativeInventoryEffect{e}, false)
			if !g.Blocked || len(g.Obligations) != 1 || g.Obligations[0].PostingNumber != effect.PostingNumber {
				t.Fatal("pending movement did not fence its physical pool")
			}
			other := pool
			other.ItemID = "independent"
			if (Guard{}).WithNativeEffects(other, source, now, now, []orders.NativeInventoryEffect{e}, false).Blocked {
				t.Fatal("independent item blocked")
			}
			other = pool
			other.Tenant = "other"
			if (Guard{}).WithNativeEffects(other, source, now, now, []orders.NativeInventoryEffect{e}, false).Blocked {
				t.Fatal("another merchant blocked")
			}
		})
	}
}

func TestNativeEffectReusedContentNeedsFreshCompletedTraversal(t *testing.T) {
	t.Parallel()
	pool, source, effect, now := nativeEffectFixture()
	effect.State = orders.StateReconciled
	snapshot := now.Add(-time.Hour)
	if !(Guard{}).WithNativeEffects(pool, source, snapshot, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("old source snapshot exposed stock after movement")
	}
	source.FreshTraversal = &domain.StockTraversalEvidence{OperationID: "fresh-read", ImportID: *source.ImportID, StartedAt: now.Add(-30 * time.Second), FinishedAt: now.Add(-20 * time.Second)}
	if (Guard{}).WithNativeEffects(pool, source, snapshot, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("fresh completed traversal of same content ignored")
	}
	source.FreshTraversal.ImportID++
	if !(Guard{}).WithNativeEffects(pool, source, snapshot, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("unrelated snapshot refresh released fence")
	}
	source.FreshTraversal.ImportID = *source.ImportID
	source.FreshTraversal.StartedAt = now.Add(-2 * time.Minute)
	if !(Guard{}).WithNativeEffects(pool, source, snapshot, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("read started before confirmation released fence")
	}
}

func TestNativeDraftAndExplicitRejectionDoNotInventInventoryEffects(t *testing.T) {
	t.Parallel()
	pool, source, effect, now := nativeEffectFixture()
	effect.Applicable = false
	if (Guard{}).WithNativeEffects(pool, source, now, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("draft treated as physical shipment")
	}
	effect.Applicable = true
	effect.State = orders.StateRejected
	if (Guard{}).WithNativeEffects(pool, source, now, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("proven rejection treated as unknown write")
	}
	g := (Guard{}).WithNativeEffects(pool, source, now, now, nil, true)
	if !g.Blocked || !g.Truncated {
		t.Fatal("truncated effects silently became empty")
	}
}

func TestNativeMovementGuardBlocksExistingNumericDecisionWithoutNewFormula(t *testing.T) {
	t.Parallel()
	pool, source, effect, now := nativeEffectFixture()
	quantity := "7"
	expires := now.Add(time.Minute)
	d := domain.StockDecision{Status: domain.StockReady, Quantity: &quantity, ExpiresAt: &expires}
	g := (Guard{}).WithNativeEffects(pool, source, now, now, []orders.NativeInventoryEffect{effect}, false)
	g.Apply(&d)
	if d.Status != domain.StockBlocked || d.Quantity != nil || quantity != "7" {
		t.Fatal("pending effect changed source arithmetic or left publication eligible")
	}
}

func TestNativeEffectCannotUseAnotherSourceAccountOrFutureTraversal(t *testing.T) {
	t.Parallel()
	pool, source, effect, now := nativeEffectFixture()
	effect.State = orders.StateReconciled
	source.FreshTraversal = &domain.StockTraversalEvidence{OperationID: "fresh", ImportID: *source.ImportID, StartedAt: now.Add(-30 * time.Second), FinishedAt: now.Add(-20 * time.Second)}
	source.Account = "other"
	if !(Guard{}).WithNativeEffects(pool, source, now.Add(-time.Hour), now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("foreign account source released pool fence")
	}
	source.Account = pool.MoyskladAccount
	source.FreshTraversal.FinishedAt = now.Add(time.Minute)
	if !(Guard{}).WithNativeEffects(pool, source, now.Add(-time.Hour), now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("future traversal released pool fence")
	}
}

func TestNativeEffectCannotUseUnadmittedClientSnapshotClock(t *testing.T) {
	t.Parallel()
	pool, source, effect, now := nativeEffectFixture()
	effect.State = orders.StateReconciled
	snapshot := now.Add(-time.Second)
	if !(Guard{}).WithNativeEffects(pool, source, snapshot, now, []orders.NativeInventoryEffect{effect}, false).Blocked {
		t.Fatal("client snapshot timestamp released physical effect without DB-admitted traversal")
	}
	if !NativeEffectBaselineStart(pool, source, snapshot, now).IsZero() {
		t.Fatal("missing durable traversal invented a database admission time")
	}
}
