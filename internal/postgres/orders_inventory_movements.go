package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

func loadNativeInventoryGuard(ctx context.Context, tx pgx.Tx, request domain.StockCalculationRequest, source domain.StockSourceEvidence, start, now time.Time, guard orderinventory.Guard) (orderinventory.Guard, error) {
	cutoff := orderinventory.NativeEffectBaselineStart(request.Pool, source, start, now)
	effects, truncated, err := readNativeInventoryEffects(ctx, tx, request.Pool, cutoff)
	if err != nil {
		return guard, err
	}
	return guard.WithNativeEffects(request.Pool, source, start, now, effects, truncated), nil
}

func readNativeInventoryEffects(ctx context.Context, q ordersQuerier, pool domain.StockPool, cutoff time.Time) ([]orders.NativeInventoryEffect, bool, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT ON(w.id) w.id,i.ozon_account,i.kind,w.state,i.applicable,t.store_id,t.item_kind,t.item_id,w.posting_number,d.created_at,r.observed_at FROM orders_native_document_targets t JOIN orders_native_document_intents i ON i.work_id=t.work_id AND i.tenant_id=t.tenant_id AND i.moysklad_account=t.moysklad_account AND i.ozon_account=t.ozon_account JOIN orders_work w ON w.id=i.work_id AND w.tenant_id=i.tenant_id AND w.moysklad_account=i.moysklad_account AND w.ozon_account=i.ozon_account LEFT JOIN orders_write_intents d ON d.work_id=w.id LEFT JOIN orders_native_document_results r ON r.work_id=w.id AND r.tenant_id=i.tenant_id AND r.moysklad_account=i.moysklad_account AND r.ozon_account=i.ozon_account WHERE t.tenant_id=$1 AND t.moysklad_account=$2 AND t.store_id=$3 AND t.item_kind=$4 AND t.item_id=$5 AND i.applicable AND w.state NOT IN ('REJECTED','BLOCKED') AND (w.state<>'RECONCILED' OR r.observed_at IS NULL OR r.observed_at>=$6) ORDER BY w.id LIMIT 1001`, pool.Tenant, pool.MoyskladAccount, pool.WarehouseID, pool.ItemKind, pool.ItemID, cutoff)
	if err != nil {
		return nil, false, ordersError("read native movement pool effects", err)
	}
	defer rows.Close()
	effects := make([]orders.NativeInventoryEffect, 0, 100)
	for rows.Next() {
		effect := orders.NativeInventoryEffect{Scope: orders.Scope{Tenant: pool.Tenant, MoyskladAccount: pool.MoyskladAccount}}
		var dispatched, reconciled *time.Time
		if err = rows.Scan(&effect.WorkID, &effect.Scope.OzonAccount, &effect.Kind, &effect.State, &effect.Applicable, &effect.StoreID, &effect.ItemKind, &effect.ItemID, &effect.PostingNumber, &dispatched, &reconciled); err != nil {
			return nil, false, ordersError("scan native movement pool effect", err)
		}
		if dispatched != nil {
			effect.DispatchedAt = *dispatched
		}
		if reconciled != nil {
			effect.ReconciledAt = *reconciled
		}
		effects = append(effects, effect)
	}
	return effects, len(effects) > 1000, ordersRowsError("read bounded native movement effects", rows.Err())
}
