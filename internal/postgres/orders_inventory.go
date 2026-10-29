package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
)

// OrderInventoryGuard reads persisted order attribution for one physical pool.
// It is an administrative read, not authentication or an available-stock API.
// Stock review uses loadOrderInventoryGuard inside its existing transaction.
func (s *Store) OrderInventoryGuard(ctx context.Context, request domain.StockCalculationRequest, baselineImport int64, now time.Time) (orderinventory.Guard, error) {
	if err := validateStockRequest(request); err != nil || now.IsZero() || baselineImport < 0 {
		return orderinventory.Guard{}, errors.New("valid stock request, baseline and time are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return orderinventory.Guard{}, fmt.Errorf("begin order inventory guard: %w", err)
	}
	defer rollbackTransaction(tx)
	source, err := readOrderInventoryBaseline(ctx, tx, request, baselineImport)
	if err != nil {
		return orderinventory.Guard{}, err
	}
	guard, err := loadOrderInventoryGuard(ctx, tx, request, source, now)
	if err != nil {
		return guard, err
	}
	return guard, ordersRowsError("commit order inventory guard", tx.Commit(ctx))
}

func loadOrderInventoryGuard(ctx context.Context, tx pgx.Tx, request domain.StockCalculationRequest, source domain.StockSourceEvidence, now time.Time) (orderinventory.Guard, error) {
	input := orderinventory.Input{Pool: request.Pool, Baseline: source, Now: now, MaxAge: request.Policy.MaxObservationAge}
	var err error
	input.Observations, input.Truncated, err = readOrderInventoryHeads(ctx, tx, request.Pool)
	if err != nil || len(input.Observations) == 0 {
		return orderinventory.Assess(input), err
	}
	input.BaselineStartedAt, err = orderInventoryBaselineStart(ctx, tx, request.Pool, source.ImportID)
	if err != nil {
		return orderinventory.Guard{}, err
	}
	if err = readOrderInventoryNative(ctx, tx, &input); err != nil {
		return orderinventory.Guard{}, err
	}
	return orderinventory.Assess(input), nil
}

func readOrderInventoryBaseline(ctx context.Context, tx pgx.Tx, request domain.StockCalculationRequest, importID int64) (domain.StockSourceEvidence, error) {
	pool := request.Pool
	source := domain.StockSourceEvidence{Tenant: pool.Tenant, Account: pool.MoyskladAccount, WarehouseID: pool.WarehouseID, ItemKind: pool.ItemKind, ItemID: pool.ItemID}
	if importID == 0 {
		return source, nil
	}
	var scope, physical, reserve string
	var observed time.Time
	err := tx.QueryRow(ctx, `SELECT i.report_scope,p.stock::text,p.reserve::text,p.observed_at FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id JOIN source_warehouse_stock_observations p ON p.import_id=i.id AND p.kind=$4 AND p.external_id=$5 AND p.warehouse_id=$6 WHERE i.id=$2 AND i.source='moysklad' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at)`, pool.Tenant, importID, pool.MoyskladAccount, pool.ItemKind, pool.ItemID, pool.WarehouseID).Scan(&scope, &physical, &reserve, &observed)
	if errors.Is(err, pgx.ErrNoRows) {
		return source, nil
	}
	if err != nil {
		return source, fmt.Errorf("read order inventory baseline: %w", err)
	}
	source.ImportID, source.ObservationFound = &importID, true
	source.Physical, source.Reserve, source.ObservedAt = &physical, &reserve, &observed
	source.TimeBasis = "local_batch_finished"
	source.TraversalComplete = strings.Contains(scope, "stock/bystore;stockMode=all;groupBy=variant")
	return source, nil
}

func orderInventoryBaselineStart(ctx context.Context, tx pgx.Tx, pool domain.StockPool, importID *int64) (time.Time, error) {
	if importID == nil {
		return time.Time{}, nil
	}
	var started time.Time
	err := tx.QueryRow(ctx, `SELECT i.started_at FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.id=$2 AND i.source='moysklad' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at)`, pool.Tenant, *importID, pool.MoyskladAccount).Scan(&started)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return started, ordersRowsError("read order inventory baseline time", err)
}
