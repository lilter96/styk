package postgres

import (
	"context"
	"fmt"
	"time"
)

type WarehouseStockRow struct {
	Kind       string    `json:"kind"`
	ItemID     string    `json:"item_id"`
	Name       string    `json:"name"`
	Article    string    `json:"article"`
	Stock      *string   `json:"stock"`
	Reserve    *string   `json:"reserve"`
	Incoming   *string   `json:"incoming"`
	Quantity   *string   `json:"quantity"`
	Present    *string   `json:"present"`
	Reserved   *string   `json:"reserved"`
	FreeStock  *string   `json:"free_stock"`
	ObservedAt time.Time `json:"observed_at"`
}

type WarehouseStockPage struct {
	Items   []WarehouseStockRow `json:"items"`
	HasNext bool                `json:"has_next"`
	Offset  int                 `json:"offset"`
}

// Quantities stay decimal strings; sources are never joined to each other.
func (s *Store) WarehouseStock(ctx context.Context, tenant, source, account, warehouse, search string, offset int) (WarehouseStockPage, error) {
	result := WarehouseStockPage{Items: make([]WarehouseStockRow, 0, 50), Offset: offset}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	query := moyWarehouseStockSQL
	if source == "ozon" {
		query = ozonWarehouseStockSQL
	}
	rows, err := s.Pool.Query(ctx, query, tenant, source, account, warehouse, offset, search)
	if err != nil {
		return result, fmt.Errorf("read warehouse stock: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v WarehouseStockRow
		if err = rows.Scan(&v.Kind, &v.ItemID, &v.Name, &v.Article, &v.Stock, &v.Reserve, &v.Incoming, &v.Quantity, &v.Present, &v.Reserved, &v.FreeStock, &v.ObservedAt); err != nil {
			return result, fmt.Errorf("scan warehouse stock: %w", err)
		}
		result.Items = append(result.Items, v)
	}
	if err = rows.Err(); err != nil {
		return result, fmt.Errorf("iterate warehouse stock: %w", err)
	}
	result.HasNext = len(result.Items) > 50
	if result.HasNext {
		result.Items = result.Items[:50]
	}
	return result, nil
}

const moyWarehouseStockSQL = `WITH latest AS (
 SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 WHERE i.source=$2 AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1)
 SELECT s.kind,s.external_id,coalesce(e.name,''),coalesce(nullif(e.article,''),e.code,''),s.stock::text,s.reserve::text,s.in_transit::text,s.quantity::text,NULL::text,NULL::text,NULL::text,s.observed_at
 FROM source_warehouse_stock_observations s JOIN latest l ON l.id=s.import_id
 LEFT JOIN source_entities e ON e.import_id=l.id AND e.kind=s.kind AND e.external_id=s.external_id
 WHERE s.warehouse_id=$4 AND ($6='' OR strpos(lower(coalesce(e.name,'')),lower($6))>0 OR strpos(lower(coalesce(e.article,'')),lower($6))>0 OR strpos(lower(coalesce(e.code,'')),lower($6))>0 OR strpos(lower(s.external_id),lower($6))>0)
 ORDER BY s.kind,s.external_id LIMIT 51 OFFSET $5`

const ozonWarehouseStockSQL = `WITH latest AS (
 SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed'
 JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 WHERE i.source=$2 AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1)
 SELECT 'product'::text,s.product_id,coalesce(e.name,''),s.offer_id,NULL::text,NULL::text,NULL::text,NULL::text,s.present::text,s.reserved::text,s.free_stock::text,s.observed_at
 FROM ozon_fbs_stock_observations s JOIN latest l ON l.id=s.import_id JOIN ozon_product_observations e ON e.import_id=l.id AND e.product_id=s.product_id
 WHERE s.warehouse_id=$4 AND ($6='' OR strpos(lower(coalesce(e.name,'')),lower($6))>0 OR strpos(lower(s.offer_id),lower($6))>0 OR strpos(lower(coalesce(s.sku,'')),lower($6))>0 OR strpos(lower(s.product_id),lower($6))>0)
 ORDER BY s.product_id LIMIT 51 OFFSET $5`
