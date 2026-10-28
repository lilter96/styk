package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/styk/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CheckCustomerOrderAccount(ctx context.Context, tenant, account string) error {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(account) == "" {
		return errors.New("tenant and account are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var ok bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2)`, tenant, account).Scan(&ok); err != nil {
		return fmt.Errorf("check account registration: %w", err)
	}
	if !ok {
		return errors.New("MoySklad account is not registered to this tenant")
	}
	return nil
}

func (s *Store) SaveCustomerOrders(ctx context.Context, b domain.CustomerOrderImport) (int64, error) {
	if strings.TrimSpace(b.Tenant) == "" || strings.TrimSpace(b.Account) == "" || b.Since.IsZero() || !b.Until.After(b.Since) || b.Until.Sub(b.Since) > 31*24*time.Hour || b.StartedAt.IsZero() || b.FinishedAt.Before(b.StartedAt) {
		return 0, errors.New("invalid customer order import")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin customer order import: %w", err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "moysklad:"+b.Account); err != nil {
		return 0, fmt.Errorf("lock customer order account: %w", err)
	}
	var batch int64
	err = tx.QueryRow(ctx, `INSERT INTO moy_order_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status) VALUES($1,$2,$3,$4,$5,$6,'completed') RETURNING id`, b.Tenant, b.Account, b.Since, b.Until, b.StartedAt, b.FinishedAt).Scan(&batch)
	if err != nil {
		return 0, fmt.Errorf("insert customer order batch: %w", err)
	}
	for _, o := range b.Orders {
		if o.ID == "" || o.Account != b.Account || o.ObservedAt.IsZero() {
			return 0, errors.New("invalid scoped customer order observation")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO moy_order_identities(tenant_id,account_id,order_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, b.Tenant, b.Account, o.ID); err != nil {
			return 0, fmt.Errorf("insert customer order identity: %w", err)
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO moy_order_observations(batch_id,tenant_id,account_id,order_id,sync_id,state_id,warehouse_id,source_created,source_updated,source_deleted,source_moment,applicable,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`, batch, b.Tenant, b.Account, o.ID, o.SyncID, o.StateID, o.WarehouseID, o.Created, o.Updated, o.Deleted, o.Moment, o.Applicable, o.ObservedAt).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert customer order observation: %w", err)
		}
		for _, p := range o.Positions {
			if p.ID == "" || p.ObservedAt.IsZero() || (p.AssortmentKind == "") != (p.AssortmentID == "") {
				return 0, errors.New("invalid customer order position")
			}
			_, err = tx.Exec(ctx, `INSERT INTO moy_order_positions(observation_id,tenant_id,account_id,position_id,assortment_kind,assortment_id,quantity,reserve,shipped,pack_id,pack_unit_id,pack_quantity,observed_at) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7::numeric,$8::numeric,$9::numeric,$10,$11,$12::numeric,$13)`, id, b.Tenant, b.Account, p.ID, p.AssortmentKind, p.AssortmentID, p.Quantity, p.Reserve, p.Shipped, p.PackID, p.PackUnitID, p.PackQuantity, p.ObservedAt)
			if err != nil {
				return 0, fmt.Errorf("insert customer order position: %w", err)
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit customer order import: %w", err)
	}
	return batch, nil
}
