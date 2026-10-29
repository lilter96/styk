package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/doctor"
	"github.com/lilter96/styk/internal/domain"
)

// Doctor reads a consistent local view. Scope is trusted administrative input.
func (s *Store) Doctor(ctx context.Context, scope MatchScope, now time.Time, maxAge time.Duration) (doctor.Report, error) {
	if strings.TrimSpace(scope.Tenant) == "" || strings.TrimSpace(scope.Tenant) != scope.Tenant || strings.TrimSpace(scope.MoyskladAccount) != scope.MoyskladAccount || strings.TrimSpace(scope.OzonAccount) != scope.OzonAccount || maxAge <= 0 {
		return doctor.Report{}, errors.New("tenant and positive maximum age required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return doctor.Report{}, err
	}
	defer rollbackTransaction(tx)
	e := doctor.Evidence{}
	refs := make([]domain.StockConnectionReference, 2)
	for i, c := range []struct {
		source, account string
		ok              *bool
	}{{"moysklad", scope.MoyskladAccount, &e.MoyskladRegistered}, {"ozon", scope.OzonAccount, &e.OzonRegistered}} {
		if c.account == "" {
			continue
		}
		ref := domain.StockConnectionReference{Source: c.source, Account: c.account}
		err = tx.QueryRow(ctx, `SELECT registered_at,include_import_history FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3`, scope.Tenant, c.source, c.account).Scan(&ref.RegisteredAt, &ref.IncludeImportHistory)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return doctor.Report{}, err
		}
		*c.ok = true
		refs[i] = ref
	}
	var mi, oi, pb, ob int64
	for i, d := range []struct {
		source string
		ref    domain.StockConnectionReference
		id     *int64
		b      *doctor.Batch
	}{{"moysklad", refs[0], &mi, &e.Moysklad}, {"ozon", refs[1], &oi, &e.Ozon}} {
		if (i == 0 && !e.MoyskladRegistered) || (i == 1 && !e.OzonRegistered) {
			continue
		}
		err = tx.QueryRow(ctx, `SELECT id,finished_at,report_scope FROM source_imports WHERE source=$1 AND account_id=$2 AND ($3 OR started_at >= $4) ORDER BY id DESC LIMIT 1`, d.source, d.ref.Account, d.ref.IncludeImportHistory, d.ref.RegisteredAt).Scan(d.id, &d.b.ObservedAt, &d.b.Scope)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return doctor.Report{}, err
		}
		d.b.Present = true
		if i == 0 {
			d.b.Completed = true
		} else {
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_import_batches WHERE import_id=$1 AND traversal_status='completed')`, *d.id).Scan(&d.b.Completed); err != nil {
				return doctor.Report{}, err
			}
		}
		d.b.ID = *d.id
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE source=$1 AND account_id=$2 AND ($3 OR started_at >= $4)`, d.source, d.ref.Account, d.ref.IncludeImportHistory, d.ref.RegisteredAt).Scan(&d.b.AvailableBatches); err != nil {
			return doctor.Report{}, err
		}
	}
	for _, d := range []struct {
		table, account string
		id             *int64
		b              *doctor.Batch
	}{{"ozon_posting_batches", scope.OzonAccount, &pb, &e.Postings}, {"moy_order_batches", scope.MoyskladAccount, &ob, &e.Orders}} {
		if (d.table == "ozon_posting_batches" && !e.OzonRegistered) || (d.table == "moy_order_batches" && !e.MoyskladRegistered) {
			continue
		}
		err = tx.QueryRow(ctx, `SELECT id,finished_at,window_from,window_to,traversal_status='completed' FROM `+d.table+` WHERE tenant_id=$1 AND account_id=$2 ORDER BY id DESC LIMIT 1`, scope.Tenant, d.account).Scan(d.id, &d.b.ObservedAt, &d.b.WindowFrom, &d.b.WindowTo, &d.b.Completed)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return doctor.Report{}, err
		}
		d.b.Present = true
		d.b.ID = *d.id
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM `+d.table+` WHERE tenant_id=$1 AND account_id=$2`, scope.Tenant, d.account).Scan(&d.b.AvailableBatches); err != nil {
			return doctor.Report{}, err
		}
	}
	for _, d := range []struct {
		b        *doctor.Batch
		id       int64
		key, sql string
	}{
		{&e.Moysklad, mi, "products", `SELECT count(*) FROM source_entities WHERE import_id=$1 AND kind='product'`}, {&e.Moysklad, mi, "variants", `SELECT count(*) FROM source_entities WHERE import_id=$1 AND kind='variant'`}, {&e.Moysklad, mi, "warehouses", `SELECT count(*) FROM source_entities WHERE import_id=$1 AND kind='store'`}, {&e.Moysklad, mi, "stock_rows", `SELECT count(*) FROM source_warehouse_stock_observations WHERE import_id=$1`},
		{&e.Ozon, oi, "products", `SELECT count(*) FROM ozon_product_observations WHERE import_id=$1`}, {&e.Ozon, oi, "warehouses", `SELECT count(*) FROM ozon_warehouse_observations WHERE import_id=$1`}, {&e.Ozon, oi, "stock_rows", `SELECT count(*) FROM ozon_fbs_stock_observations WHERE import_id=$1`}, {&e.Postings, pb, "postings", `SELECT count(*) FROM ozon_posting_observations WHERE batch_id=$1`}, {&e.Orders, ob, "orders", `SELECT count(*) FROM moy_order_observations WHERE batch_id=$1`}, {&e.Orders, ob, "positions", `SELECT count(*) FROM moy_order_positions p JOIN moy_order_observations o ON o.id=p.observation_id WHERE o.batch_id=$1`}} {
		if d.b.Counts == nil {
			d.b.Counts = map[string]int64{}
		}
		if d.id == 0 {
			continue
		}
		var n int64
		if err = tx.QueryRow(ctx, d.sql, d.id).Scan(&n); err != nil {
			return doctor.Report{}, err
		}
		d.b.Counts[d.key] = n
	}
	if !e.MoyskladRegistered || !e.OzonRegistered {
		if err = tx.Commit(ctx); err != nil {
			return doctor.Report{}, err
		}
		return doctor.Evaluate(e, now, maxAge), nil
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&e.ProductMappings); err != nil {
		return doctor.Report{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND status='active'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&e.WarehouseMappings); err != nil {
		return doctor.Report{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM ozon_product_observations p WHERE import_id=$1 AND NOT EXISTS(SELECT 1 FROM product_mappings m WHERE m.tenant_id=$2 AND m.moysklad_account=$3 AND m.ozon_account=$4 AND m.ozon_product_id=p.product_id)`, oi, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&e.UnconfirmedProducts); err != nil {
		return doctor.Report{}, err
	}
	// Catalog progress is a separate, bounded-scope fact. Historical mappings
	// remain in ProductMappings, but cannot inflate the latest eligible catalog.
	// Use the same completed-batch eligibility as the product review endpoint.
	var catalogProducts, catalogConfirmed int64
	err = tx.QueryRow(ctx, `WITH latest AS (
		SELECT i.id FROM source_imports i
		JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed'
		WHERE i.source='ozon' AND i.account_id=$1 AND ($2 OR i.started_at >= $3)
		ORDER BY i.id DESC LIMIT 1
	) SELECT count(p.product_id),count(p.product_id) FILTER (WHERE EXISTS (
		SELECT 1 FROM product_mappings m WHERE m.tenant_id=$4 AND m.moysklad_account=$5
		AND m.ozon_account=$1 AND m.ozon_product_id=p.product_id AND m.ozon_offer_id=p.offer_id
	)) FROM latest l LEFT JOIN ozon_product_observations p ON p.import_id=l.id GROUP BY l.id`, scope.OzonAccount, refs[1].IncludeImportHistory, refs[1].RegisteredAt, scope.Tenant, scope.MoyskladAccount).Scan(&catalogProducts, &catalogConfirmed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return doctor.Report{}, err
	}
	if err == nil {
		e.CatalogProducts, e.CatalogConfirmedProducts = &catalogProducts, &catalogConfirmed
	}
	if pb > 0 && ob > 0 {
		rows, er := tx.Query(ctx, `SELECT id FROM ozon_posting_observations WHERE batch_id=$1 ORDER BY id LIMIT 11`, pb)
		if er != nil {
			return doctor.Report{}, er
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if er = rows.Scan(&id); er != nil {
				rows.Close()
				return doctor.Report{}, er
			}
			ids = append(ids, id)
		}
		er = rows.Err()
		rows.Close()
		if er != nil {
			return doctor.Report{}, er
		}
		e.CorrelationsChecked = true
		e.CorrelationsTruncated = len(ids) > 10
		if len(ids) > 10 {
			ids = ids[:10]
		}
		e.CorrelationStatuses = map[string]int64{}
		for _, id := range ids {
			c, er := correlateCustomerOrder(ctx, tx, OrderCorrelationScope{scope.Tenant, scope.MoyskladAccount, scope.OzonAccount}, id, ob)
			if er != nil {
				return doctor.Report{}, er
			}
			e.CorrelationStatuses[string(c.Status)]++
			e.CorrelationsTruncated = e.CorrelationsTruncated || c.Truncated
			if c.Status == domain.OrderAmbiguous {
				e.AmbiguousCorrelations++
			}
		}
	}
	// Evaluate one deterministic mapped pool. Remaining pools are explicitly unchecked.
	r := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount}, Target: domain.StockTarget{OzonAccount: scope.OzonAccount}, Policy: domain.StockCalculationPolicy{Version: 1, Unit: "piece", SafetyStock: "0", MaxObservationAge: maxAge, DecisionLifetime: time.Minute}}
	err = tx.QueryRow(ctx, `SELECT m.target_kind,m.target_id,m.ozon_product_id,w.moysklad_warehouse_id,w.ozon_warehouse_id FROM product_mappings m CROSS JOIN warehouse_mappings w WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND w.tenant_id=m.tenant_id AND w.moysklad_account=m.moysklad_account AND w.ozon_account=m.ozon_account AND w.status='active' ORDER BY m.id,w.id LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&r.Pool.ItemKind, &r.Pool.ItemID, &r.Target.ProductID, &r.Pool.WarehouseID, &r.Target.WarehouseID)
	if err == nil {
		ev := domain.StockEvidence{Connections: refs, Proofs: map[string]domain.StockProof{}}
		if err = loadStockMappings(ctx, tx, r, now, &ev); err != nil {
			return doctor.Report{}, err
		}
		if err = loadStockObservations(ctx, tx, r, &ev); err != nil {
			return doctor.Report{}, err
		}
		if err = loadStockCustomerOrders(ctx, tx, r, &ev); err != nil {
			return doctor.Report{}, err
		}
		d := commitment.Evaluate(r, ev, now)
		e.CalculationReferences = map[string]int64{}
		if ev.ProductMapping != nil {
			e.CalculationReferences["product_mapping_id"] = ev.ProductMapping.ID
			e.CalculationReferences["product_mapping_version"] = int64(ev.ProductMapping.Version)
		}
		if ev.WarehouseMapping != nil {
			e.CalculationReferences["warehouse_mapping_id"] = ev.WarehouseMapping.ID
			e.CalculationReferences["warehouse_mapping_version"] = int64(ev.WarehouseMapping.Version)
		}
		e.DecisionStatus = string(d.Status)
		e.DecisionReasons = d.ReasonCodes
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return doctor.Report{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return doctor.Report{}, err
	}
	return doctor.Evaluate(e, now, maxAge), nil
}
