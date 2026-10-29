package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

// ListPostings returns only the requested tenant and seller account. Correlations
// are computed from current confirmed mappings, not mapping history.
func (s *Store) ListPostings(ctx context.Context, scope domain.PostingReadScope) ([]domain.PostingView, error) {
	if strings.TrimSpace(scope.Tenant) == "" || strings.TrimSpace(scope.Account) == "" || strings.TrimSpace(scope.MoyskladAccount) == "" {
		return nil, errors.New("tenant and both account IDs required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin posting read: %w", err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT b.id,o.id,o.posting_number,o.order_id,o.order_number,o.status,o.substatus,o.warehouse_id,o.in_process_at,o.shipment_date,o.delivering_date,o.parent_posting_number,o.fact_delivery_date,o.shipment_date_without_delay,o.observed_at,o.observed_time_basis,o.source_created_at,o.source_updated_at,o.cancellation_present,o.cancellation_reason,o.cancellation_reason_id,o.cancellation_type,o.cancellation_initiator,o.cancelled_after_ship, wm.moysklad_account,wm.moysklad_warehouse_id,wm.version
 FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id
 LEFT JOIN warehouse_mappings wm ON wm.tenant_id=b.tenant_id AND wm.ozon_account=b.account_id AND wm.moysklad_account=$3 AND wm.ozon_warehouse_id=o.warehouse_id AND wm.status='active' AND o.warehouse_id<>''
 WHERE b.tenant_id=$1 AND b.account_id=$2 AND b.traversal_status='completed' ORDER BY b.id,o.id`, scope.Tenant, scope.Account, scope.MoyskladAccount)
	if err != nil {
		return nil, fmt.Errorf("read postings: %w", err)
	}
	out := []domain.PostingView{}
	positions := map[int64]int{}
	for rows.Next() {
		var v domain.PostingView
		v.Tenant = scope.Tenant
		v.Account = scope.Account
		var present *bool
		var c domain.PostingCancellation
		if err = rows.Scan(&v.BatchID, &v.ObservationID, &v.Observation.PostingNumber, &v.Observation.OrderID, &v.Observation.OrderNumber, &v.Observation.Status, &v.Observation.Substatus, &v.Observation.WarehouseID, &v.Observation.InProcessAt, &v.Observation.ShipmentDate, &v.Observation.DeliveringDate, &v.Observation.ParentPostingNumber, &v.Observation.FactDeliveryDate, &v.Observation.ShipmentDateWithoutDelay, &v.Observation.ObservedAt, &v.Observation.ObservedTimeBasis, &v.Observation.SourceCreatedAt, &v.Observation.SourceUpdatedAt, &present, &c.Reason, &c.ReasonID, &c.Type, &c.Initiator, &c.CancelledAfterShip, &v.Warehouse.MoyskladAccount, &v.Warehouse.WarehouseID, &v.Warehouse.MappingVersion); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode posting: %w", err)
		}
		if present != nil {
			v.Observation.CancellationPresenceKnown = true
		}
		if (present != nil && *present) || (present == nil && (c.Reason != nil || c.ReasonID != nil || c.Type != nil || c.Initiator != nil || c.CancelledAfterShip != nil)) {
			v.Observation.Cancellation = &c
		}
		v.Warehouse.Status = "unmatched"
		if v.Warehouse.WarehouseID != nil {
			v.Warehouse.Status = "confirmed"
		}
		v.Lines = []domain.PostingLineView{}
		positions[v.ObservationID] = len(out)
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("read postings: %w", err)
	}
	rows, err = tx.Query(ctx, `SELECT o.id,l.offer_id,l.sku,l.quantity,m.moysklad_account,m.target_kind,m.target_id,m.version,p.offer_id,p.sku
 FROM ozon_posting_batches b JOIN ozon_posting_observations o ON o.batch_id=b.id JOIN ozon_posting_lines l ON l.observation_id=o.id
 LEFT JOIN product_mappings m ON m.tenant_id=b.tenant_id AND m.ozon_account=b.account_id AND m.moysklad_account=$3 AND m.ozon_offer_id=l.offer_id AND l.offer_id<>''
 LEFT JOIN LATERAL (SELECT id FROM source_imports si JOIN ozon_import_batches ob ON ob.import_id=si.id AND ob.traversal_status='completed' WHERE si.source='ozon' AND si.account_id=b.account_id ORDER BY si.id DESC LIMIT 1) latest ON true
 LEFT JOIN ozon_product_observations p ON p.import_id=latest.id AND p.product_id=m.ozon_product_id
 WHERE b.tenant_id=$1 AND b.account_id=$2 AND b.traversal_status='completed' ORDER BY b.id,o.id,l.line_no`, scope.Tenant, scope.Account, scope.MoyskladAccount)
	if err != nil {
		return nil, fmt.Errorf("read posting lines: %w", err)
	}
	for rows.Next() {
		var obs int64
		var l domain.PostingLineView
		var ma, kind, target *string
		var version *int
		var productOffer, productSKU *string
		if err = rows.Scan(&obs, &l.OfferID, &l.SKU, &l.Quantity, &ma, &kind, &target, &version, &productOffer, &productSKU); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode line: %w", err)
		}
		l.Correlation.Status = "unmatched"
		if ma != nil {
			l.Correlation.Status = "unverified"
			if productOffer != nil && *productOffer != l.OfferID {
				l.Correlation.Status = "conflict"
			} else if productSKU != nil && l.SKU != "" && *productSKU != "" {
				if *productSKU == l.SKU && productOffer != nil {
					l.Correlation.Status = "confirmed"
					l.Correlation.MoyskladAccount = ma
					l.Correlation.TargetKind = kind
					l.Correlation.TargetID = target
					l.Correlation.MappingVersion = version
				} else if *productSKU != l.SKU {
					l.Correlation.Status = "conflict"
				}
			}
		}
		index, ok := positions[obs]
		if !ok {
			rows.Close()
			return nil, errors.New("line outside scoped observations")
		}
		out[index].Lines = append(out[index].Lines, l)
		out[index].Observation.Lines = append(out[index].Observation.Lines, l.PostingLine)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("read posting lines: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit posting read: %w", err)
	}
	return out, nil
}
