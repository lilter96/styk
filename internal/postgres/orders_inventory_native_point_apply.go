package postgres

import (
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

func applyInventoryNativePoint(pool domain.StockPool, observation *orderinventory.Observation, read nativePointInventoryRead) {
	observation.NativePointAdmittedAt, observation.NativePointRecordedAt = read.AdmittedAt, read.RecordedAt
	observation.NativeReadCode, observation.Positions, observation.NativeSyncID = read.Code, nil, nil
	point := read.Point
	if point == nil || read.Code != "" {
		return
	}
	observation.NativeStartedAt, observation.NativeFinishedAt = point.StartedAt, point.FinishedAt
	syncID := point.Correlation.SyncID
	observation.NativeSyncID = &syncID
	for _, position := range point.Positions {
		if position.MoyskladKind != pool.ItemKind || position.MoyskladID != pool.ItemID {
			continue
		}
		if position.CompositionSafe.IsKnown() && !*position.CompositionSafe.Value {
			observation.NativeReadCode = "NATIVE_PACK_UNIT_UNVERIFIED"
		}
		observation.Positions = append(observation.Positions, nativePointStockPosition(*point, position))
	}
}

func nativePointStockPosition(point orders.NativeOrderPoint, position orders.TargetPosition) domain.StockObservedOrderPosition {
	quantity, warehouse, state := string(position.Quantity), point.StoreID, point.StateID
	result := domain.StockObservedOrderPosition{OrderID: point.Correlation.CustomerOrderID, PositionID: position.ID, ItemKind: position.MoyskladKind, ItemID: position.MoyskladID, WarehouseID: &warehouse, StateID: &state, Quantity: &quantity, Reserve: nativePointStockDecimal(position.Reserve), Shipped: nativePointStockDecimal(position.Shipped), ObservedAt: point.ObservedAt}
	if point.Applicable.IsKnown() {
		value := *point.Applicable.Value
		result.Applicable = &value
	}
	if point.ProviderUpdatedAt.IsKnown() {
		value := *point.ProviderUpdatedAt.Value
		result.SourceUpdated = &value
	}
	if point.SourceDeleted.IsKnown() {
		value := *point.SourceDeleted.Value
		result.SourceDeleted = &value
	}
	return result
}

func nativePointStockDecimal(field orders.Field[orders.Decimal]) *string {
	if !field.IsKnown() {
		return nil
	}
	value := string(*field.Value)
	return &value
}
