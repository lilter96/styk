package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

func loadStockCustomerOrders(ctx context.Context, tx pgx.Tx, req domain.StockCalculationRequest, e *domain.StockEvidence) error {
	summary := &domain.StockCustomerOrderEvidence{}
	e.CustomerOrders = summary
	var batch int64
	err := tx.QueryRow(ctx, `SELECT id FROM moy_order_batches WHERE tenant_id=$1 AND account_id=$2 AND traversal_status='completed' ORDER BY id DESC LIMIT 1`, req.Pool.Tenant, req.Pool.MoyskladAccount).Scan(&batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read customer order evidence batch: %w", err)
	}
	summary.BatchID = &batch
	if e.Marketplace.PostingBatchID == nil || e.ProductMapping == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT o.id FROM ozon_posting_observations o JOIN ozon_posting_lines l ON l.observation_id=o.id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND o.warehouse_id=$4 AND l.offer_id=$5 ORDER BY o.id LIMIT 11`, *e.Marketplace.PostingBatchID, req.Pool.Tenant, req.Target.OzonAccount, req.Target.WarehouseID, e.ProductMapping.OfferID)
	if err != nil {
		return fmt.Errorf("read posting evidence identities: %w", err)
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("decode posting evidence identity: %w", err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read posting evidence identities: %w", err)
	}
	if len(ids) > 10 {
		summary.Truncated = true
		ids = ids[:10]
	}
	scope := OrderCorrelationScope{Tenant: req.Pool.Tenant, MoyskladAccount: req.Pool.MoyskladAccount, OzonAccount: req.Target.OzonAccount}
	for _, id := range ids {
		correlation, err := correlateCustomerOrder(ctx, tx, scope, id, batch)
		if err != nil {
			return fmt.Errorf("correlate customer order evidence: %w", err)
		}
		summary.Correlations = append(summary.Correlations, correlation)
	}
	return nil
}
