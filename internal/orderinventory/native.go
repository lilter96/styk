package orderinventory

import (
	"math"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func nativeUnits(input Input, observation Observation) (int64, int64, bool) {
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
	return sumNativePositions(input, observation, positions)
}

func sumNativePositions(input Input, observation Observation, wanted map[string]orders.TargetPosition) (int64, int64, bool) {
	var reserve, shipped int64
	seen := make(map[string]bool, len(observation.Positions))
	for _, position := range observation.Positions {
		target, found := wanted[position.PositionID]
		if !found || seen[position.PositionID] || !nativePositionScoped(input, observation, position) {
			return 0, 0, false
		}
		r, s, valid := positionUnits(position, target)
		if !valid || reserve > math.MaxInt64-r || shipped > math.MaxInt64-s {
			return 0, 0, false
		}
		reserve, shipped = reserve+r, shipped+s
		seen[position.PositionID] = true
	}
	return reserve, shipped, len(seen) > 0 && len(seen) == len(wanted)
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
