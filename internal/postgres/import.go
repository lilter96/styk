package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

func (s *Store) SaveInventoryImport(ctx context.Context, snap domain.InventoryImport, fingerprint string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin import: %w", err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "moysklad:"+snap.Account); err != nil {
		return false, fmt.Errorf("lock import: %w", err)
	}
	var previous string
	err = tx.QueryRow(ctx, `SELECT fingerprint FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1`, snap.Account).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("check previous import: %w", err)
	}
	if err == nil && previous == fingerprint {
		return false, nil
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('moysklad',$1,$2,$3,$4,'stock/all;stockMode=all;quantityMode=all | stock/bystore;stockMode=all;groupBy=variant') RETURNING id`, snap.Account, fingerprint, snap.StartedAt, snap.FinishedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert import: %w", err)
	}
	for _, e := range snap.Entities {
		_, err = tx.Exec(ctx, `INSERT INTO source_entities(import_id,kind,external_id,name,code,article,parent_id,source_updated) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, e.Kind, e.ID, e.Name, e.Code, e.Article, e.ParentID, e.SourceUpdated)
		if err != nil {
			return false, fmt.Errorf("insert source entity: %w", err)
		}
	}
	for _, o := range snap.Stock {
		_, err = tx.Exec(ctx, `INSERT INTO source_stock_observations(import_id,kind,external_id,source_href,stock,reserve,in_transit,quantity,free_stock) VALUES($1,$2,$3,$4,$5::numeric,$6::numeric,$7::numeric,$8::numeric,$9::numeric)`, id, o.Kind, o.ExternalID, o.SourceHref, o.Stock, o.Reserve, o.InTransit, o.Quantity, o.FreeStock)
		if err != nil {
			return false, fmt.Errorf("insert stock observation: %w", err)
		}
	}
	for _, o := range snap.WarehouseStock {
		_, err = tx.Exec(ctx, `INSERT INTO source_warehouse_stock_observations(import_id,kind,external_id,source_href,warehouse_id,warehouse_href,stock,reserve,in_transit,quantity,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7::numeric,$8::numeric,$9::numeric,$10::numeric,$11)`, id, o.Kind, o.ExternalID, o.SourceHref, o.WarehouseID, o.WarehouseHref, o.Stock, o.Reserve, o.InTransit, o.Quantity, snap.FinishedAt)
		if err != nil {
			return false, fmt.Errorf("insert warehouse stock observation: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit import: %w", err)
	}
	return true, nil
}
