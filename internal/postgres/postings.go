package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/lilter96/styk/internal/domain"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SavePostings(ctx context.Context, b domain.PostingImport) (int64, error) {
	if strings.TrimSpace(b.Tenant) == "" || strings.TrimSpace(b.Account) == "" || b.StartedAt.IsZero() || b.FinishedAt.IsZero() || !b.Until.After(b.Since) {
		return 0, errors.New("invalid posting batch")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	var batch int64
	err = tx.QueryRow(ctx, `INSERT INTO ozon_posting_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status) VALUES($1,$2,$3,$4,$5,$6,'completed') RETURNING id`, b.Tenant, b.Account, b.Since, b.Until, b.StartedAt, b.FinishedAt).Scan(&batch)
	if err != nil {
		return 0, fmt.Errorf("insert posting batch: %w", err)
	}
	for _, p := range b.Postings {
		if p.ObservedAt.IsZero() || p.ObservedTimeBasis != "detail_received" {
			return 0, errors.New("posting detail observation time and basis required")
		}
		var reason, reasonID, cancelType, initiator, after any
		var presence *bool
		if p.CancellationPresenceKnown || p.Cancellation != nil {
			present := p.Cancellation != nil
			presence = &present
		}
		if p.Cancellation != nil {
			reason = p.Cancellation.Reason
			reasonID = p.Cancellation.ReasonID
			cancelType = p.Cancellation.Type
			initiator = p.Cancellation.Initiator
			after = p.Cancellation.CancelledAfterShip
		}
		// Row locking and the scoped key prevent concurrent imports assigning
		// one posting number to different external orders.
		var identityOrder string
		err = tx.QueryRow(ctx, `INSERT INTO ozon_posting_identities(tenant_id,account_id,posting_number,order_id) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id,account_id,posting_number) DO UPDATE SET order_id=EXCLUDED.order_id WHERE ozon_posting_identities.order_id=EXCLUDED.order_id RETURNING order_id`, b.Tenant, b.Account, p.PostingNumber, p.OrderID).Scan(&identityOrder)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errors.New("posting number already belongs to a different order")
		}
		if err != nil {
			return 0, fmt.Errorf("save posting identity: %w", err)
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO ozon_posting_observations(batch_id,posting_number,order_id,order_number,status,substatus,warehouse_id,in_process_at,shipment_date,delivering_date,cancellation_reason,cancellation_reason_id,cancellation_type,cancellation_initiator,cancelled_after_ship,observed_at,source_created_at,source_updated_at,cancellation_present,observed_time_basis,parent_posting_number,fact_delivery_date,shipment_date_without_delay,tenant_id,account_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25) RETURNING id`, batch, p.PostingNumber, p.OrderID, p.OrderNumber, p.Status, p.Substatus, p.WarehouseID, p.InProcessAt, p.ShipmentDate, p.DeliveringDate, reason, reasonID, cancelType, initiator, after, p.ObservedAt, p.SourceCreatedAt, p.SourceUpdatedAt, presence, p.ObservedTimeBasis, p.ParentPostingNumber, p.FactDeliveryDate, p.ShipmentDateWithoutDelay, b.Tenant, b.Account).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert posting: %w", err)
		}
		for i, l := range p.Lines {
			if _, err = tx.Exec(ctx, `INSERT INTO ozon_posting_lines(observation_id,line_no,offer_id,sku,quantity) VALUES($1,$2,$3,$4,$5)`, id, i, l.OfferID, l.SKU, l.Quantity); err != nil {
				return 0, fmt.Errorf("insert posting line: %w", err)
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit postings: %w", err)
	}
	return batch, nil
}
