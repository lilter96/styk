package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

func (s *Store) SaveOzonImport(ctx context.Context, snap domain.OzonImport, fingerprint string) (bool, error) {
	if snap.Account == "" {
		return false, errors.New("Ozon account required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin Ozon import: %w", err)
	}
	defer tx.Rollback(context.Background())
	_, created, err := saveOzonImportTx(ctx, tx, snap, fingerprint, nil)
	if err != nil {
		return false, err
	}
	if !created {
		return false, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit Ozon import: %w", err)
	}
	return true, nil
}

// saveOzonImportTx is shared by the CLI and fenced worker. The caller owns
// commit and must not perform network requests while the transaction is open.
// notBefore excludes historical imports hidden by the tenant's registration.
func saveOzonImportTx(ctx context.Context, tx pgx.Tx, snap domain.OzonImport, fingerprint string, notBefore *time.Time) (int64, bool, error) {
	if snap.Account == "" {
		return 0, false, errors.New("Ozon account required")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+snap.Account); err != nil {
		return 0, false, fmt.Errorf("lock Ozon import: %w", err)
	}
	var previous string
	var previousID int64
	var previousStarted time.Time
	err := tx.QueryRow(ctx, `SELECT id,fingerprint,started_at FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1`, snap.Account).Scan(&previousID, &previous, &previousStarted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("read previous Ozon import: %w", err)
	}
	if err == nil && previous == fingerprint && (notBefore == nil || !previousStarted.Before(*notBefore)) {
		// Only a completed batch may be referenced as successful.
		var completed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_import_batches WHERE import_id=$1 AND traversal_status='completed')`, previousID).Scan(&completed); err != nil {
			return 0, false, fmt.Errorf("read Ozon batch: %w", err)
		}
		if completed {
			return previousID, false, nil
		}
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('ozon',$1,$2,$3,$4,'product/list ALL+ARCHIVED;warehouse/list;stocks-by-warehouse/fbs') RETURNING id`, snap.Account, fingerprint, snap.StartedAt, snap.FinishedAt).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("insert Ozon import: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ozon_import_batches(import_id,traversal_status) VALUES($1,'completed')`, id); err != nil {
		return 0, false, fmt.Errorf("insert Ozon batch: %w", err)
	}
	for _, p := range snap.Products {
		_, err = tx.Exec(ctx, `INSERT INTO ozon_product_observations(import_id,product_id,offer_id,sku,archived) VALUES($1,$2,$3,NULLIF($4,''),$5)`, id, p.ProductID, p.OfferID, p.SKU, p.Archived)
		if err != nil {
			return 0, false, fmt.Errorf("insert Ozon product: %w", err)
		}
	}
	for _, w := range snap.Warehouses {
		_, err = tx.Exec(ctx, `INSERT INTO ozon_warehouse_observations(import_id,warehouse_id,name,status,warehouse_type,is_rfbs) VALUES($1,$2,$3,$4,$5,$6)`, id, w.ID, w.Name, w.Status, w.WarehouseType, w.IsRFBS)
		if err != nil {
			return 0, false, fmt.Errorf("insert Ozon warehouse: %w", err)
		}
	}
	for _, p := range snap.Stocks {
		_, err = tx.Exec(ctx, `INSERT INTO ozon_fbs_stock_observations(import_id,product_id,offer_id,sku,warehouse_id,warehouse_name,present,reserved,free_stock,observed_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10)`, id, p.ProductID, p.OfferID, p.SKU, p.WarehouseID, p.WarehouseName, p.Present, p.Reserved, p.FreeStock, snap.FinishedAt)
		if err != nil {
			return 0, false, fmt.Errorf("insert Ozon stock: %w", err)
		}
	}
	return id, true, nil
}
