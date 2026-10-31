package postgres

import (
	"context"
	"encoding/json"
	"fmt"
)

type MappingSearch struct {
	Query, SearchBy         string
	ActiveOnly, InStockOnly bool
	Page, PageSize, Offset  int
}

type MappingSearchPage struct {
	Items       json.RawMessage `json:"items"`
	Total       int             `json:"total"`
	Page        int             `json:"page"`
	PageSize    int             `json:"pageSize"`
	Offset      int             `json:"offset"`
	HasNext     bool            `json:"has_next"`
	HasNextPage bool            `json:"hasNextPage"`
}

// One statement counts and pages the same tenant-scoped snapshot.
func (s *Store) SearchMappingTargets(ctx context.Context, tenant, moy, ozon string, v MappingSearch) (MappingSearchPage, error) {
	result := MappingSearchPage{Page: v.Page, PageSize: v.PageSize, Offset: v.Offset}
	err := s.Pool.QueryRow(ctx, mappingSearchSQL, tenant, moy, ozon, v.Query, v.SearchBy, v.InStockOnly, v.PageSize, v.Offset).Scan(&result.Items, &result.Total)
	if err != nil {
		return result, fmt.Errorf("search mapping targets: %w", err)
	}
	result.HasNext = v.Offset+v.PageSize < result.Total
	result.HasNextPage = result.HasNext
	return result, nil
}

const mappingSearchSQL = `WITH latest AS (
 SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1
), filtered AS (
 SELECT e.kind,e.external_id AS id,e.name,e.article,e.code,e.parent_id,s.quantity::text AS quantity,
 EXISTS(SELECT 1 FROM product_mappings m WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND m.target_kind=e.kind AND m.target_id=e.external_id) AS mapped
 FROM source_entities e JOIN latest l ON l.id=e.import_id
 LEFT JOIN source_stock_observations s ON s.import_id=e.import_id AND s.kind=e.kind AND s.external_id=e.external_id
 WHERE e.kind IN ('product','variant') AND (NOT $6 OR s.quantity>0)
 AND ($4='' OR ($5 IN ('all','id') AND strpos(lower(e.external_id),lower($4))>0)
 OR ($5 IN ('all','name') AND strpos(lower(e.name),lower($4))>0)
 OR ($5 IN ('all','article') AND strpos(lower(e.article),lower($4))>0)
 OR ($5 IN ('all','code') AND strpos(lower(e.code),lower($4))>0))
), page AS (SELECT * FROM filtered ORDER BY kind,id LIMIT $7 OFFSET $8)
SELECT COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.kind,p.id) FROM page p),'[]'::jsonb), (SELECT count(*) FROM filtered)`
