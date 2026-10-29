package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/ordercorrelation"
)

type OrderCorrelationScope struct{ Tenant, MoyskladAccount, OzonAccount string }

// CorrelateCustomerOrder uses an explicitly selected posting observation and
// customer-order traversal. It is a trusted administrative read, not auth.
func (s *Store) CorrelateCustomerOrder(ctx context.Context, scope OrderCorrelationScope, postingID, batchID int64) (domain.OrderCorrelation, error) {
	if scope.Tenant == "" || scope.MoyskladAccount == "" || scope.OzonAccount == "" || postingID <= 0 || batchID <= 0 {
		return domain.OrderCorrelation{}, errors.New("tenant, both accounts, posting observation and order batch are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.OrderCorrelation{}, fmt.Errorf("begin correlation: %w", err)
	}
	defer rollbackTransaction(tx)
	r, err := correlateCustomerOrder(ctx, tx, scope, postingID, batchID)
	if err != nil {
		return r, err
	}
	if err = tx.Commit(ctx); err != nil {
		return r, fmt.Errorf("commit correlation read: %w", err)
	}
	return r, nil
}

func correlateCustomerOrder(ctx context.Context, tx pgx.Tx, scope OrderCorrelationScope, postingID, batchID int64) (domain.OrderCorrelation, error) {
	r := domain.OrderCorrelation{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, PostingObservationID: postingID, CustomerOrderBatchID: batchID}
	var accounts int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&accounts)
	if err != nil {
		return r, fmt.Errorf("check correlation scope: %w", err)
	}
	if accounts != 2 {
		return r, errors.New("both accounts must be registered to this tenant")
	}
	var warehouse string
	err = tx.QueryRow(ctx, `SELECT posting_number,warehouse_id FROM ozon_posting_observations WHERE id=$1 AND tenant_id=$2 AND account_id=$3`, postingID, scope.Tenant, scope.OzonAccount).Scan(&r.PostingNumber, &warehouse)
	if err != nil {
		return r, fmt.Errorf("read scoped posting: %w", err)
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM moy_order_batches WHERE id=$1 AND tenant_id=$2 AND account_id=$3 AND traversal_status='completed')`, batchID, scope.Tenant, scope.MoyskladAccount).Scan(&found)
	if err != nil || !found {
		if err != nil {
			return r, fmt.Errorf("read order batch: %w", err)
		}
		return r, errors.New("customer order batch not found in scope")
	}
	var wmID int64
	var wmVersion int
	var sourceWarehouse string
	err = tx.QueryRow(ctx, `SELECT w.id,w.version,w.moysklad_warehouse_id FROM warehouse_mappings w JOIN warehouse_mapping_history h ON h.mapping_id=w.id AND h.version=w.version AND h.tenant_id=w.tenant_id AND h.ozon_account=w.ozon_account AND h.moysklad_account=w.moysklad_account AND h.moysklad_warehouse_id=w.moysklad_warehouse_id AND h.ozon_warehouse_id=w.ozon_warehouse_id AND h.status='active' AND h.provenance=w.provenance AND h.confirmed_at=w.confirmed_at WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.ozon_warehouse_id=$4 AND w.status='active'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, warehouse).Scan(&wmID, &wmVersion, &sourceWarehouse)
	warehouseOK := err == nil && warehouse != ""
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("read correlation warehouse mapping: %w", err)
	}
	type product struct {
		id         int64
		version    int
		kind, item string
	}
	products := map[string][]product{}
	productSeen := map[int64]bool{}
	complete := warehouseOK
	conflict := false
	rows, err := tx.Query(ctx, `SELECT l.offer_id,l.sku,m.id,m.version,m.target_kind,m.target_id,h.mapping_id,p.offer_id,p.sku FROM ozon_posting_lines l JOIN ozon_posting_observations o ON o.id=l.observation_id LEFT JOIN product_mappings m ON m.tenant_id=o.tenant_id AND m.ozon_account=o.account_id AND m.moysklad_account=$4 AND m.ozon_offer_id=l.offer_id AND l.offer_id<>'' LEFT JOIN product_mapping_history h ON h.mapping_id=m.id AND h.version=m.version AND h.tenant_id=m.tenant_id AND h.ozon_account=m.ozon_account AND h.moysklad_account=m.moysklad_account AND h.target_kind=m.target_kind AND h.target_id=m.target_id AND h.ozon_product_id=m.ozon_product_id AND h.ozon_offer_id=m.ozon_offer_id AND h.provenance=m.provenance AND h.confirmed_at=m.confirmed_at LEFT JOIN LATERAL (SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$2 AND c.source='ozon' AND c.account_id=i.account_id WHERE i.source='ozon' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1) latest ON true LEFT JOIN ozon_product_observations p ON p.import_id=latest.id AND p.product_id=m.ozon_product_id WHERE l.observation_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 ORDER BY l.line_no`, postingID, scope.Tenant, scope.OzonAccount, scope.MoyskladAccount)
	if err != nil {
		return r, fmt.Errorf("read correlation products: %w", err)
	}
	lineCount := 0
	for rows.Next() {
		lineCount++
		var offer, sku string
		var mid, hid *int64
		var version *int
		var kind, item, catalogOffer, catalogSKU *string
		if err = rows.Scan(&offer, &sku, &mid, &version, &kind, &item, &hid, &catalogOffer, &catalogSKU); err != nil {
			rows.Close()
			return r, fmt.Errorf("decode correlation product: %w", err)
		}
		if mid == nil || hid == nil || version == nil || kind == nil || item == nil || catalogOffer == nil || catalogSKU == nil || sku == "" || *catalogSKU == "" {
			if mid != nil && hid == nil {
				conflict = true
			}
			complete = false
			continue
		}
		if *catalogOffer != offer || *catalogSKU != sku {
			complete = false
			conflict = true
			continue
		}
		if !productSeen[*mid] {
			products[*kind+":"+*item] = append(products[*kind+":"+*item], product{*mid, *version, *kind, *item})
			productSeen[*mid] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, fmt.Errorf("read correlation lines: %w", err)
	}
	if lineCount == 0 {
		complete = false
	}
	rows, err = tx.Query(ctx, `SELECT o.id,o.order_id,o.warehouse_id,o.source_updated,o.source_deleted,o.applicable,p.position_id,p.assortment_kind,p.assortment_id,p.quantity::text,p.reserve::text,p.shipped::text,p.observed_at FROM moy_order_observations o JOIN moy_order_positions p ON p.observation_id=o.id AND p.tenant_id=o.tenant_id AND p.account_id=o.account_id WHERE o.batch_id=$1 AND o.tenant_id=$2 AND o.account_id=$3 ORDER BY o.order_id,p.position_id`, batchID, scope.Tenant, scope.MoyskladAccount)
	if err != nil {
		return r, fmt.Errorf("read scoped customer orders: %w", err)
	}
	byOrder := map[int64]*domain.OrderCorrelationCandidate{}
	positionCount := 0
	for rows.Next() {
		var oid int64
		var orderID, posID string
		var wh, updated, deleted, kind, item, qty, reserve, shipped *string
		var applicable *bool
		var observed time.Time
		if err = rows.Scan(&oid, &orderID, &wh, &updated, &deleted, &applicable, &posID, &kind, &item, &qty, &reserve, &shipped, &observed); err != nil {
			rows.Close()
			return r, fmt.Errorf("decode order evidence: %w", err)
		}
		if kind == nil || item == nil {
			continue
		}
		matched, ok := products[*kind+":"+*item]
		if !ok {
			continue
		}
		if wh == nil || *wh == "" {
			complete = false
			continue
		}
		if !warehouseOK || *wh != sourceWarehouse {
			continue
		}
		c := byOrder[oid]
		if c == nil {
			if len(byOrder) >= 100 {
				r.Truncated = true
				complete = false
				continue
			}
			c = &domain.OrderCorrelationCandidate{OrderObservationID: oid, OrderID: orderID, WarehouseID: wh, SourceUpdated: updated, SourceDeleted: deleted, Applicable: applicable, WarehouseMappingID: wmID, WarehouseMappingVersion: wmVersion}
			byOrder[oid] = c
		}
		for _, p := range matched {
			if positionCount >= 1000 {
				r.Truncated = true
				complete = false
				break
			}
			c.Positions = append(c.Positions, domain.OrderPositionEvidence{PositionID: posID, AssortmentKind: *kind, AssortmentID: *item, Quantity: qty, Reserve: reserve, Shipped: shipped, ObservedAt: observed.UTC().Format(time.RFC3339Nano), ProductMappingID: p.id, ProductMappingVersion: p.version})
			positionCount++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, fmt.Errorf("read order evidence: %w", err)
	}
	candidates := make([]domain.OrderCorrelationCandidate, 0, len(byOrder))
	for _, c := range byOrder {
		uncertain, err := orderHistoryConflict(ctx, tx, scope, c)
		if err != nil {
			return r, err
		}
		c.HistoryConflict = uncertain
		candidates = append(candidates, *c)
	}
	return ordercorrelation.Evaluate(ordercorrelation.Input{Correlation: r, MappingsComplete: complete, MappingsConflict: conflict, Candidates: candidates}, nil), nil
}

// Local insertion order is never used as source chronology. Identical repeated
// observations are harmless; newer or incompatible source evidence is uncertain.
func orderHistoryConflict(ctx context.Context, tx pgx.Tx, scope OrderCorrelationScope, c *domain.OrderCorrelationCandidate) (bool, error) {
	parse := func(value *string) (time.Time, bool) {
		if value == nil {
			return time.Time{}, false
		}
		for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
			if t, err := time.ParseInLocation(layout, *value, time.FixedZone("MSK", 3*60*60)); err == nil {
				// Older Moscow timestamps can cross historical offset changes;
				// without an explicit offset their ordering is not established.
				if t.Year() < 2015 {
					return time.Time{}, false
				}
				return t, true
			}
		}
		return time.Time{}, false
	}
	selected, selectedKnown := parse(c.SourceUpdated)
	rows, err := tx.Query(ctx, `SELECT source_updated FROM moy_order_observations WHERE tenant_id=$1 AND account_id=$2 AND order_id=$3 AND id<>$4 LIMIT 1001`, scope.Tenant, scope.MoyskladAccount, c.OrderID, c.OrderObservationID)
	if err != nil {
		return false, fmt.Errorf("read order history: %w", err)
	}
	count := 0
	for rows.Next() {
		var updated *string
		if err = rows.Scan(&updated); err != nil {
			rows.Close()
			return false, fmt.Errorf("decode order history: %w", err)
		}
		count++
		other, known := parse(updated)
		if count > 1000 || (updated != nil && !known) || (c.SourceUpdated != nil && !selectedKnown) || (selectedKnown && known && (other.After(selected) || (other.Equal(selected) && *updated != *c.SourceUpdated))) {
			rows.Close()
			return true, nil
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, fmt.Errorf("read order history: %w", err)
	}
	if count == 0 {
		return false, nil
	}
	var different bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM moy_order_observations x JOIN moy_order_observations s ON s.id=$1 WHERE x.tenant_id=$2 AND x.account_id=$3 AND x.order_id=$4 AND x.id<>s.id AND (x.source_updated IS NULL OR s.source_updated IS NULL OR x.source_updated=s.source_updated) AND (ROW(x.warehouse_id,x.applicable,x.source_deleted,x.state_id,x.sync_id,x.source_created,x.source_moment) IS DISTINCT FROM ROW(s.warehouse_id,s.applicable,s.source_deleted,s.state_id,s.sync_id,s.source_created,s.source_moment) OR EXISTS(SELECT position_id,assortment_kind,assortment_id,quantity,reserve,shipped,pack_id,pack_unit_id,pack_quantity FROM moy_order_positions WHERE observation_id=x.id EXCEPT SELECT position_id,assortment_kind,assortment_id,quantity,reserve,shipped,pack_id,pack_unit_id,pack_quantity FROM moy_order_positions WHERE observation_id=s.id) OR EXISTS(SELECT position_id,assortment_kind,assortment_id,quantity,reserve,shipped,pack_id,pack_unit_id,pack_quantity FROM moy_order_positions WHERE observation_id=s.id EXCEPT SELECT position_id,assortment_kind,assortment_id,quantity,reserve,shipped,pack_id,pack_unit_id,pack_quantity FROM moy_order_positions WHERE observation_id=x.id)))`, c.OrderObservationID, scope.Tenant, scope.MoyskladAccount, c.OrderID).Scan(&different)
	if err != nil {
		return false, fmt.Errorf("compare order history: %w", err)
	}
	return different, nil
}
