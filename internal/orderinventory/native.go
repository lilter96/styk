package orderinventory

import (
	"math"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func nativeUnits(input Input, observation Observation) (int64, int64, bool) {
	if observation.Work != nil && observation.Work.Command.Action.IsNativeMovement() {
		return nativeDemandUnits(input, observation)
	}
	target := observation.Target
	if target == nil || target.Fields.StoreID != input.Pool.WarehouseID || observation.NativeSyncID == nil || *observation.NativeSyncID != observation.Binding.SyncID {
		return 0, 0, false
	}
	positions := make(map[string]orders.TargetPosition, len(target.Positions))
	for _, position := range target.Positions {
		if position.MoyskladKind != input.Pool.ItemKind || position.MoyskladID != input.Pool.ItemID {
			continue
		}
		if _, exists := positions[position.ID]; exists || position.ID == "" {
			return 0, 0, false
		}
		positions[position.ID] = position
	}
	return sumOwnedNativePositions(input, observation, positions, ownedNativePositionIDs(input, observation))
}

// The complete native pool frontier is still checked. Only an independently
// reconstructed source/UUID binding can exclude a foreign line from this order's
// obligation; its reserve remains in the authoritative source baseline.
func ownedNativePositionIDs(input Input, observation Observation) map[string]bool {
	work, target := observation.Work, observation.Target
	if work == nil || target == nil || observation.SplitSuperseded != nil || knownStatus(observation.Posting, orders.SupersededSplitParentStatus) || knownStatus(observation.Posting, "cancelled") {
		return nil
	}
	baseline, err := orders.ProjectionPositionBaseline(*work, *target)
	if work.Command.Action == orders.ActionMirrorUpdate && work.Command.PositionBaseline == nil && observation.PositionOwnership != nil {
		baseline, err = orders.LegacyScalarPositionBaseline(*work, *target, *observation.PositionOwnership)
	}
	if err != nil {
		return nil // Preserve conservative whole-pool attribution without UUID proof.
	}
	owned := make(map[string]bool, len(baseline.Bindings))
	for _, binding := range baseline.Bindings {
		if binding.Source.Mapping.MoyskladKind == input.Pool.ItemKind && binding.Source.Mapping.MoyskladID == input.Pool.ItemID && hasLine(observation.Lines, binding.Source.Identity) {
			owned[binding.Native.ID] = true
		}
	}
	if len(owned) == 0 {
		return nil
	}
	return owned
}

func sumNativePositions(input Input, observation Observation, wanted map[string]orders.TargetPosition) (int64, int64, bool) {
	return sumOwnedNativePositions(input, observation, wanted, nil)
}

func sumOwnedNativePositions(input Input, observation Observation, wanted map[string]orders.TargetPosition, owned map[string]bool) (int64, int64, bool) {
	var reserve, shipped int64
	ownedCount := 0
	seen := make(map[string]bool, len(observation.Positions))
	for _, position := range observation.Positions {
		target, found := wanted[position.PositionID]
		if !found || seen[position.PositionID] || !nativePositionScoped(input, observation, position) {
			return 0, 0, false
		}
		r, s, valid := positionUnits(position, target)
		if !valid {
			return 0, 0, false
		}
		seen[position.PositionID] = true
		if owned != nil && !owned[position.PositionID] {
			continue
		}
		if reserve > math.MaxInt64-r || shipped > math.MaxInt64-s {
			return 0, 0, false
		}
		reserve, shipped = reserve+r, shipped+s
		ownedCount++
	}
	return reserve, shipped, len(seen) > 0 && len(seen) == len(wanted) && (owned == nil || ownedCount == len(owned))
}

// Coverage includes foreign positions even when their reserves do not belong to
// this posting's obligation. Across distinct native orders the stock baseline
// must cover the full observed pool reserve, not merely the owned subset.
func nativeReserveCoverage(observation Observation) (int64, bool) {
	var coverage int64
	for _, position := range observation.Positions {
		if position.Reserve == nil {
			return 0, false
		}
		reserve, ok := whole(*position.Reserve)
		if !ok || coverage > math.MaxInt64-reserve {
			return 0, false
		}
		coverage += reserve
	}
	return coverage, true
}

func nativePositionScoped(input Input, observation Observation, position domain.StockObservedOrderPosition) bool {
	if position.OrderID != observation.Binding.CustomerOrderID || position.ItemKind != input.Pool.ItemKind || position.ItemID != input.Pool.ItemID || position.WarehouseID == nil || *position.WarehouseID != input.Pool.WarehouseID || position.Applicable == nil {
		return false
	}
	if position.SourceDeleted != nil && *position.SourceDeleted != "" {
		return false
	}
	return !position.ObservedAt.IsZero() && !position.ObservedAt.Before(observation.NativeStartedAt) && !position.ObservedAt.After(observation.NativeFinishedAt)
}

func positionUnits(position domain.StockObservedOrderPosition, target orders.TargetPosition) (int64, int64, bool) {
	if position.Quantity == nil || position.Reserve == nil || position.Shipped == nil || !target.Reserve.IsKnown() || target.Reserve.Value == nil {
		return 0, 0, false
	}
	quantity, qOK := whole(*position.Quantity)
	reserve, rOK := whole(*position.Reserve)
	shipped, sOK := whole(*position.Shipped)
	targetQuantity, tqOK := whole(string(target.Quantity))
	targetReserve, trOK := whole(string(*target.Reserve.Value))
	if !qOK || !rOK || !sOK || !tqOK || !trOK || quantity != targetQuantity || reserve != targetReserve || reserve > quantity || shipped > quantity || reserve > quantity-shipped {
		return 0, 0, false
	}
	return reserve, shipped, reserve == 0 || *position.Applicable
}
