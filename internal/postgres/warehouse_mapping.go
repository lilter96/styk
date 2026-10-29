package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"strings"
)

// ConfirmWarehouseMapping validates identities from completed imports under the same locks used by importers.
func (s *Store) ConfirmWarehouseMapping(ctx context.Context, scope MatchScope, moyID, ozonID, provenance string) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	if strings.TrimSpace(moyID) == "" || strings.TrimSpace(ozonID) == "" || strings.TrimSpace(provenance) == "" {
		return false, errors.New("warehouse IDs and provenance are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackTransaction(tx)
	for _, key := range []string{"ozon:" + scope.OzonAccount, "moysklad:" + scope.MoyskladAccount, "warehouse-mapping:" + scope.Tenant} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return false, fmt.Errorf("lock warehouse mapping: %w", err)
		}
	}
	var mi, oi int64
	if err = tx.QueryRow(ctx, `SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1`, scope.MoyskladAccount).Scan(&mi); err != nil {
		return false, fmt.Errorf("latest MoySklad import: %w", err)
	}
	if err = tx.QueryRow(ctx, `SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' WHERE i.source='ozon' AND i.account_id=$1 ORDER BY i.id DESC LIMIT 1`, scope.OzonAccount).Scan(&oi); err != nil {
		return false, fmt.Errorf("latest Ozon import: %w", err)
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT ozon_warehouse_id FROM warehouse_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND moysklad_warehouse_id=$4`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, moyID).Scan(&existing)
	if err == nil {
		if existing != ozonID {
			return false, errors.New("source warehouse already mapped")
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM source_entities WHERE import_id=$1 AND kind='store' AND external_id=$2)`, mi, moyID).Scan(&valid)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, errors.New("source warehouse missing from latest import")
	}
	valid, err = readStockWarehouseSupported(ctx, tx, domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}, oi, ozonID, true, false)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, errors.New("warehouse stock capability is unconfirmed")
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance) VALUES($1,$2,$3,$4,$5,'active',1,$6) RETURNING id`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, moyID, ozonID, provenance).Scan(&id)
	if err != nil {
		return false, fmt.Errorf("create warehouse mapping: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_history(mapping_id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,moysklad_import_id,ozon_import_id) SELECT id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,$2,$3 FROM warehouse_mappings WHERE id=$1`, id, mi, oi)
	if err != nil {
		return false, fmt.Errorf("record warehouse mapping history: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit warehouse mapping: %w", err)
	}
	return true, nil
}
