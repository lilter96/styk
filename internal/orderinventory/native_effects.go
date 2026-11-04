package orderinventory

import (
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

// WithNativeEffects adds ordering evidence to the existing guard. It never
// adds/subtracts document quantities: the provider stock baseline owns those.
func (g Guard) WithNativeEffects(pool domain.StockPool, source domain.StockSourceEvidence, snapshotStart, now time.Time, effects []orders.NativeInventoryEffect, truncated bool) Guard {
	if truncated {
		g.Truncated, g.Blocked = true, true
	}
	baselineStart := NativeEffectBaselineStart(pool, source, snapshotStart, now)
	for _, effect := range effects {
		if !nativeEffectInPool(effect, pool) || !effect.Applicable {
			continue
		}
		reason := nativeEffectBlocker(effect, source, baselineStart, now)
		if reason == "" {
			continue
		}
		g.Blocked = true
		g.Obligations = append(g.Obligations, Obligation{Scope: effect.Scope, PostingNumber: effect.PostingNumber, State: Unknown, Reason: reason})
	}
	return g
}

func nativeEffectInPool(e orders.NativeInventoryEffect, p domain.StockPool) bool {
	return e.Scope.Tenant == p.Tenant && e.Scope.MoyskladAccount == p.MoyskladAccount && e.StoreID == p.WarehouseID && e.ItemKind == p.ItemKind && e.ItemID == p.ItemID
}

func nativeEffectBlocker(e orders.NativeInventoryEffect, source domain.StockSourceEvidence, start, now time.Time) string {
	if e.WorkID == "" || e.PostingNumber == "" || (e.Kind != "demand" && e.Kind != "salesreturn") {
		return "NATIVE_DOCUMENT_EFFECT_INVALID"
	}
	if e.State == orders.StateRejected || e.State == orders.StateBlocked && e.DispatchedAt.IsZero() {
		return ""
	}
	if e.State != orders.StateReconciled {
		return "NATIVE_DOCUMENT_EFFECT_PENDING"
	}
	if !source.ObservationFound || !source.TraversalComplete || source.ImportID == nil || e.ReconciledAt.IsZero() || e.ReconciledAt.After(now) || !start.After(e.ReconciledAt) {
		return "NATIVE_DOCUMENT_STOCK_RECHECK_REQUIRED"
	}
	return ""
}

// A content-identical import can provide newer successful traversal evidence.
// Its timestamp is a local checking boundary, not a provider watermark.
func NativeEffectBaselineStart(pool domain.StockPool, source domain.StockSourceEvidence, snapshotStart, now time.Time) time.Time {
	if source.Tenant != pool.Tenant || source.Account != pool.MoyskladAccount || source.WarehouseID != pool.WarehouseID || source.ItemKind != pool.ItemKind || source.ItemID != pool.ItemID {
		return time.Time{}
	}
	if source.ImportID == nil || snapshotStart.IsZero() || snapshotStart.After(now) {
		return time.Time{}
	}
	traversal := source.FreshTraversal
	if traversal == nil {
		return time.Time{}
	}
	if traversal.OperationID == "" || traversal.ImportID != *source.ImportID || traversal.StartedAt.IsZero() || traversal.FinishedAt.Before(traversal.StartedAt) || traversal.FinishedAt.After(now) {
		return time.Time{}
	}
	return traversal.StartedAt
}
