package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

func loadStockCustomerOrders(ctx context.Context, tx pgx.Tx, req domain.StockCalculationRequest, e *domain.StockEvidence) error {
	summary := &domain.StockCustomerOrderEvidence{}
	e.CustomerOrders = summary
	var batch int64
	err := tx.QueryRow(ctx, `SELECT id,window_from,window_to,finished_at FROM moy_order_batches WHERE tenant_id=$1 AND account_id=$2 AND traversal_status='completed' ORDER BY id DESC LIMIT 1`, req.Pool.Tenant, req.Pool.MoyskladAccount).Scan(&batch, &summary.WindowFrom, &summary.WindowTo, &summary.FinishedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read customer order evidence batch: %w", err)
	}
	if err == nil {
		summary.BatchID = &batch
		summary.Positions, summary.Truncated, err = loadObservedOrderPositions(ctx, tx, req, batch)
		if err != nil {
			return err
		}
	}
	if e.Marketplace.PostingBatchID == nil || e.ProductMapping == nil {
		return nil
	}
	summary.PostingBatchID = e.Marketplace.PostingBatchID
	summary.PostingWindowFrom = e.Marketplace.PostingWindowFrom
	summary.PostingWindowTo = e.Marketplace.PostingWindowTo
	postings, truncated, err := loadObservedPostingLines(ctx, tx, req, *e.Marketplace.PostingBatchID, e.ProductMapping.OfferID)
	if err != nil {
		return err
	}
	summary.Postings = postings
	summary.Truncated = summary.Truncated || truncated
	if summary.BatchID == nil {
		return nil
	}
	correlations, truncated, err := loadOrderCorrelations(ctx, tx, req, *e.Marketplace.PostingBatchID, batch, e.ProductMapping.OfferID)
	if err != nil {
		return err
	}
	summary.Correlations = correlations
	summary.Truncated = summary.Truncated || truncated
	return nil
}

func loadObservedOrderPositions(ctx context.Context, tx pgx.Tx, req domain.StockCalculationRequest, batch int64) ([]domain.StockObservedOrderPosition, bool, error) {
	rows, err := tx.Query(ctx, `SELECT o.order_id,p.position_id,p.assortment_kind,p.assortment_id,o.warehouse_id,o.state_id,o.source_updated,o.source_deleted,o.source_moment,o.applicable,p.quantity::text,p.reserve::text,p.shipped::text,p.observed_at FROM moy_order_observations o JOIN moy_order_positions p ON p.observation_id=o.id AND p.tenant_id=o.tenant_id AND p.account_id=o.account_id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND p.assortment_kind=$4 AND p.assortment_id=$5 AND (o.warehouse_id=$6 OR o.warehouse_id IS NULL) ORDER BY o.id,p.position_id LIMIT 21`, batch, req.Pool.Tenant, req.Pool.MoyskladAccount, req.Pool.ItemKind, req.Pool.ItemID, req.Pool.WarehouseID)
	if err != nil {
		return nil, false, fmt.Errorf("read observed order positions: %w", err)
	}
	defer rows.Close()
	positions := make([]domain.StockObservedOrderPosition, 0, 20)
	for rows.Next() {
		var p domain.StockObservedOrderPosition
		if err = rows.Scan(&p.OrderID, &p.PositionID, &p.ItemKind, &p.ItemID, &p.WarehouseID, &p.StateID, &p.SourceUpdated, &p.SourceDeleted, &p.SourceMoment, &p.Applicable, &p.Quantity, &p.Reserve, &p.Shipped, &p.ObservedAt); err != nil {
			return nil, false, fmt.Errorf("decode observed order position: %w", err)
		}
		positions = append(positions, p)
	}
	if err = rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read observed order positions: %w", err)
	}
	if len(positions) > 20 {
		return positions[:20], true, nil
	}
	return positions, false, nil
}

func loadObservedPostingLines(ctx context.Context, tx pgx.Tx, req domain.StockCalculationRequest, batch int64, offer string) ([]domain.StockObservedPostingLine, bool, error) {
	rows, err := tx.Query(ctx, `SELECT o.posting_number,o.status,o.warehouse_id,l.offer_id,l.sku,l.quantity,o.observed_at,o.source_created_at,o.source_updated_at FROM ozon_posting_observations o JOIN ozon_posting_lines l ON l.observation_id=o.id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND (o.warehouse_id=$4 OR o.warehouse_id='') AND l.offer_id=$5 ORDER BY o.id,l.line_no LIMIT 21`, batch, req.Pool.Tenant, req.Target.OzonAccount, req.Target.WarehouseID, offer)
	if err != nil {
		return nil, false, fmt.Errorf("read observed posting lines: %w", err)
	}
	defer rows.Close()
	postings := make([]domain.StockObservedPostingLine, 0, 20)
	for rows.Next() {
		var p domain.StockObservedPostingLine
		if err = rows.Scan(&p.PostingNumber, &p.Status, &p.WarehouseID, &p.OfferID, &p.SKU, &p.Quantity, &p.ObservedAt, &p.SourceCreatedAt, &p.SourceUpdatedAt); err != nil {
			return nil, false, fmt.Errorf("decode observed posting line: %w", err)
		}
		postings = append(postings, p)
	}
	if err = rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read observed posting lines: %w", err)
	}
	if len(postings) > 20 {
		return postings[:20], true, nil
	}
	return postings, false, nil
}

func loadOrderCorrelations(ctx context.Context, tx pgx.Tx, req domain.StockCalculationRequest, postingBatch, orderBatch int64, offer string) ([]domain.OrderCorrelation, bool, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT o.id FROM ozon_posting_observations o JOIN ozon_posting_lines l ON l.observation_id=o.id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND o.warehouse_id=$4 AND l.offer_id=$5 ORDER BY o.id LIMIT 11`, postingBatch, req.Pool.Tenant, req.Target.OzonAccount, req.Target.WarehouseID, offer)
	if err != nil {
		return nil, false, fmt.Errorf("read posting evidence identities: %w", err)
	}
	ids := make([]int64, 0, 11)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, fmt.Errorf("decode posting evidence identity: %w", err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, fmt.Errorf("read posting evidence identities: %w", err)
	}
	truncated := len(ids) > 10
	if truncated {
		ids = ids[:10]
	}
	scope := OrderCorrelationScope{Tenant: req.Pool.Tenant, MoyskladAccount: req.Pool.MoyskladAccount, OzonAccount: req.Target.OzonAccount}
	correlations := make([]domain.OrderCorrelation, 0, len(ids))
	for _, id := range ids {
		v, e := correlateCustomerOrder(ctx, tx, scope, id, orderBatch)
		if e != nil {
			return nil, false, fmt.Errorf("correlate customer order evidence: %w", e)
		}
		correlations = append(correlations, v)
	}
	return correlations, truncated, nil
}

// StockOrderEvidence exposes bounded observations for one confirmed mapping.
// It deliberately does not infer order coverage or non-overlap from a window.
func (s *Store) StockOrderEvidence(ctx context.Context, scope domain.StockSyncScope, product, warehouse string) (*domain.StockCustomerOrderEvidence, error) {
	e, _, err := s.stockOrderEvidenceSnapshot(ctx, scope, product, warehouse, false)
	return e, err
}

// StockOrderEvidenceWithProgress reads completed evidence and progress from
// the same snapshot, so a concurrent seal cannot produce a mixed response.
func (s *Store) StockOrderEvidenceWithProgress(ctx context.Context, scope domain.StockSyncScope, product, warehouse string) (*domain.StockCustomerOrderEvidence, *OrderEvidenceSummary, error) {
	return s.stockOrderEvidenceSnapshot(ctx, scope, product, warehouse, true)
}

func (s *Store) stockOrderEvidenceSnapshot(ctx context.Context, scope domain.StockSyncScope, product, warehouse string, includeProgress bool) (*domain.StockCustomerOrderEvidence, *OrderEvidenceSummary, error) {
	if !scope.Valid() || !positiveAPIID(product) || !positiveAPIID(warehouse) {
		return nil, nil, domain.ErrStockSyncNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Warn("could not roll back order-evidence transaction")
		}
	}()
	e, err := loadStockOrderEvidenceSnapshot(ctx, tx, scope, product, warehouse)
	if err != nil {
		return nil, nil, err
	}
	var progress *OrderEvidenceSummary
	if includeProgress {
		progress, err = currentOrderEvidence(ctx, tx, scope)
		if err != nil {
			return nil, nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit order-evidence read: %w", err)
	}
	return e, progress, nil
}

func loadStockOrderEvidenceSnapshot(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, product, warehouse string) (*domain.StockCustomerOrderEvidence, error) {
	req, offer, err := stockOrderTarget(ctx, tx, scope, product, warehouse)
	if err != nil {
		return nil, err
	}
	e := domain.StockEvidence{ProductMapping: &domain.MappingReference{OfferID: offer}}
	var batch int64
	var from, to time.Time
	err = tx.QueryRow(ctx, `SELECT id,window_from,window_to FROM ozon_posting_batches WHERE tenant_id=$1 AND account_id=$2 AND traversal_status='completed' ORDER BY id DESC LIMIT 1`, scope.Tenant, scope.OzonAccount).Scan(&batch, &from, &to)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		e.Marketplace.PostingBatchID, e.Marketplace.PostingWindowFrom, e.Marketplace.PostingWindowTo = &batch, &from, &to
	}
	if err = loadStockCustomerOrders(ctx, tx, req, &e); err != nil {
		return nil, err
	}
	return e.CustomerOrders, nil
}

func stockOrderTarget(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, product, warehouse string) (domain.StockCalculationRequest, string, error) {
	req := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount}, Target: domain.StockTarget{OzonAccount: scope.OzonAccount, ProductID: product, WarehouseID: warehouse}}
	var offer string
	err := tx.QueryRow(ctx, `SELECT pm.target_kind,pm.target_id,wm.moysklad_warehouse_id,pm.ozon_offer_id FROM product_mappings pm JOIN warehouse_mappings wm ON wm.tenant_id=pm.tenant_id AND wm.moysklad_account=pm.moysklad_account AND wm.ozon_account=pm.ozon_account WHERE pm.tenant_id=$1 AND pm.moysklad_account=$2 AND pm.ozon_account=$3 AND pm.ozon_product_id=$4 AND wm.ozon_warehouse_id=$5 AND wm.status='active'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, product, warehouse).Scan(&req.Pool.ItemKind, &req.Pool.ItemID, &req.Pool.WarehouseID, &offer)
	if errors.Is(err, pgx.ErrNoRows) {
		return req, "", domain.ErrStockSyncNotFound
	}
	return req, offer, err
}
