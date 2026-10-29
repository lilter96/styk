package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
)

func readOrderInventoryNative(ctx context.Context, tx pgx.Tx, input *orderinventory.Input) error {
	var batch int64
	var started, finished time.Time
	err := tx.QueryRow(ctx, `SELECT id,started_at,finished_at FROM moy_order_batches WHERE tenant_id=$1 AND account_id=$2 AND traversal_status='completed' ORDER BY id DESC LIMIT 1`, input.Pool.Tenant, input.Pool.MoyskladAccount).Scan(&batch, &started, &finished)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read order inventory native batch: %w", err)
	}
	for index := range input.Observations {
		observation := &input.Observations[index]
		observation.NativeStartedAt, observation.NativeFinishedAt = started, finished
		if err = readOrderInventoryPositions(ctx, tx, input.Pool, batch, observation); err != nil {
			return err
		}
	}
	return nil
}

func readOrderInventoryPositions(ctx context.Context, tx pgx.Tx, pool domain.StockPool, batch int64, observation *orderinventory.Observation) error {
	if observation.Binding.CustomerOrderID == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT o.sync_id,o.order_id,p.position_id,p.assortment_kind,p.assortment_id,o.warehouse_id,o.state_id,o.source_updated,o.source_deleted,o.source_moment,o.applicable,p.quantity::text,p.reserve::text,p.shipped::text,p.observed_at FROM moy_order_observations o JOIN moy_order_positions p ON p.observation_id=o.id AND p.tenant_id=o.tenant_id AND p.account_id=o.account_id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND o.order_id=$4 AND p.assortment_kind=$5 AND p.assortment_id=$6 ORDER BY p.position_id LIMIT 101`, batch, pool.Tenant, pool.MoyskladAccount, observation.Binding.CustomerOrderID, pool.ItemKind, pool.ItemID)
	if err != nil {
		return fmt.Errorf("read order inventory positions: %w", err)
	}
	defer rows.Close()
	observation.Positions = make([]domain.StockObservedOrderPosition, 0, 100)
	for rows.Next() {
		var position domain.StockObservedOrderPosition
		if err = rows.Scan(&observation.NativeSyncID, &position.OrderID, &position.PositionID, &position.ItemKind, &position.ItemID, &position.WarehouseID, &position.StateID, &position.SourceUpdated, &position.SourceDeleted, &position.SourceMoment, &position.Applicable, &position.Quantity, &position.Reserve, &position.Shipped, &position.ObservedAt); err != nil {
			return fmt.Errorf("decode order inventory position: %w", err)
		}
		observation.Positions = append(observation.Positions, position)
	}
	observation.Truncated = len(observation.Positions) > 100
	return ordersRowsError("read order inventory positions", rows.Err())
}
