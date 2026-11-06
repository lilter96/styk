package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

// WarehouseReadRequest bounds every read; filters apply before pagination.
type WarehouseReadRequest struct {
	Search, Filter, Warehouse, Cursor string
	Offset, Limit                     int
}

func (r WarehouseReadRequest) Valid() bool {
	return len(r.Search) <= 256 && len(r.Warehouse) <= 256 && len(r.Cursor) <= 256 && r.Offset >= 0 && r.Offset <= 100000 && r.Limit >= 1 && r.Limit <= 100 && (r.Filter == "" || r.Filter == "mapped" || r.Filter == "unmapped" || r.Filter == "attention")
}

type WarehouseOverviewRow struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	IsRFBS        *bool      `json:"is_rfbs"`
	WarehouseType string     `json:"warehouse_type"`
	StoreID       string     `json:"store_id"`
	StoreName     string     `json:"store_name"`
	State         string     `json:"state"`
	StockRows     int64      `json:"stock_rows"`
	ObservedAt    *time.Time `json:"observed_at"`
	MappingID     *int64     `json:"mapping_id"`
	ConfirmedAt   *time.Time `json:"confirmed_at"`
}

type WarehouseCounts struct {
	Total     int `json:"total"`
	Mapped    int `json:"mapped"`
	Unmapped  int `json:"unmapped"`
	Attention int `json:"attention"`
}

type WarehouseOverviewPage struct {
	Items        []WarehouseOverviewRow `json:"items"`
	Counts       WarehouseCounts        `json:"counts"`
	Total        int                    `json:"total"`
	HasNext      bool                   `json:"has_next"`
	Offset       int                    `json:"offset"`
	OzonImported bool                   `json:"ozon_imported"`
	MoyImported  bool                   `json:"moy_imported"`
}

func validWarehouseScope(scope MatchScope) bool {
	for _, value := range []string{scope.Tenant, scope.MoyskladAccount, scope.OzonAccount} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
	}
	return true
}

// WarehouseOverview has no dependency on order configuration or provider I/O.
// One statement keeps evidence, aggregate counts and the page in one snapshot.
func (s *Store) WarehouseOverview(ctx context.Context, scope MatchScope, request WarehouseReadRequest) (WarehouseOverviewPage, error) {
	var result WarehouseOverviewPage
	if !validWarehouseScope(scope) || !request.Valid() {
		return result, ErrReviewEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var registered bool
	var raw []byte
	err := s.Pool.QueryRow(ctx, warehouseOverviewSQL, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, request.Search, request.Filter, request.Warehouse, request.Offset, request.Limit, request.Cursor, automaticScopeAllowed(domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount})).Scan(&registered, &raw)
	if err != nil {
		return result, fmt.Errorf("read warehouse overview: %w", err)
	}
	if !registered {
		return result, ErrReviewScope
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode warehouse overview: %w", err)
	}
	return result, nil
}

// History must match the complete active row and both account registrations.
// Invisible history contributes only a blocking flag, never a name or ID.
const warehouseReadCTE = `WITH connections AS (
 SELECT count(*)=2 AS registered FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))
), oi AS (
 SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed'
 JOIN stock_connections c ON c.tenant_id=$1 AND c.source='ozon' AND c.account_id=i.account_id
 WHERE i.source='ozon' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1
), mi AS (
 SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source='moysklad' AND c.account_id=i.account_id
 WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1
), ow AS (SELECT w.* FROM ozon_warehouse_observations w JOIN oi ON oi.id=w.import_id),
 mw AS (SELECT e.* FROM source_entities e JOIN mi ON mi.id=e.import_id WHERE e.kind='store'),
 m AS (SELECT m.*, EXISTS(
 SELECT 1 FROM warehouse_mapping_history h
 JOIN stock_connections mc ON mc.tenant_id=h.tenant_id AND mc.source='moysklad' AND mc.account_id=h.moysklad_account
 JOIN stock_connections oc ON oc.tenant_id=h.tenant_id AND oc.source='ozon' AND oc.account_id=h.ozon_account
 JOIN source_imports hi ON hi.id=h.moysklad_import_id AND hi.source='moysklad' AND hi.account_id=h.moysklad_account
 JOIN source_imports ho ON ho.id=h.ozon_import_id AND ho.source='ozon' AND ho.account_id=h.ozon_account
 WHERE h.mapping_id=m.id AND h.version=m.version AND h.tenant_id=m.tenant_id AND h.moysklad_account=m.moysklad_account AND h.ozon_account=m.ozon_account
 AND h.moysklad_warehouse_id=m.moysklad_warehouse_id AND h.ozon_warehouse_id=m.ozon_warehouse_id
 AND h.status=m.status AND h.provenance=m.provenance AND h.confirmed_at=m.confirmed_at
 AND (mc.include_import_history OR (hi.started_at>=mc.registered_at AND h.confirmed_at>=mc.registered_at))
 AND (oc.include_import_history OR (ho.started_at>=oc.registered_at AND h.confirmed_at>=oc.registered_at))) AS visible
 FROM warehouse_mappings m WHERE m.tenant_id=$1 AND m.moysklad_account=$2 AND m.ozon_account=$3 AND m.status='active')`

const warehouseOverviewSQL = warehouseReadCTE + `,
 stocks AS (SELECT s.warehouse_id,count(*) AS n,CASE WHEN bool_or(s.observed_at>now()) THEN max(s.observed_at) ELSE min(s.observed_at) END AS observed_at FROM ozon_fbs_stock_observations s JOIN oi ON oi.id=s.import_id GROUP BY s.warehouse_id),
 ids AS (SELECT warehouse_id AS id FROM ow UNION SELECT ozon_warehouse_id FROM m WHERE visible),
 facts AS (SELECT ids.id,coalesce(ow.name,'') AS name,ow.is_rfbs,coalesce(ow.warehouse_type,'') AS warehouse_type,
 CASE WHEN m.visible THEN m.moysklad_warehouse_id ELSE '' END AS store_id,
 CASE WHEN m.visible THEN coalesce(mw.name,'') ELSE '' END AS store_name,
 CASE WHEN m.visible THEN m.id END AS mapping_id,CASE WHEN m.visible THEN m.confirmed_at END AS confirmed_at,
 coalesce(stocks.n,0) AS stock_rows,stocks.observed_at,
 CASE WHEN m.id IS NOT NULL AND NOT m.visible THEN 'conflict'
 WHEN ow.warehouse_id IS NULL OR ow.is_rfbs IS NULL OR (m.visible AND mw.external_id IS NULL) OR NOT EXISTS(SELECT 1 FROM mi) THEN 'missing_evidence'
 WHEN NOT ((NOT ow.is_rfbs AND ow.warehouse_type IN ('','fbs')) OR (ow.is_rfbs AND ow.warehouse_type IN ('rfbs','rfbs_express') AND ow.status='created' AND $10)) THEN 'unsupported'
 WHEN m.visible THEN 'mapped' ELSE 'unmapped' END AS state
 FROM ids LEFT JOIN ow ON ow.warehouse_id=ids.id LEFT JOIN m ON m.ozon_warehouse_id=ids.id
 LEFT JOIN mw ON mw.external_id=m.moysklad_warehouse_id LEFT JOIN stocks ON stocks.warehouse_id=ids.id),
 filtered AS (SELECT * FROM facts WHERE ($6='' OR id=$6) AND ($4='' OR strpos(lower(name),lower($4))>0 OR strpos(lower(id),lower($4))>0 OR strpos(lower(store_name),lower($4))>0)
 AND ($5='' OR state=$5 OR ($5='attention' AND state NOT IN ('mapped','unmapped')))),
 page AS (SELECT * FROM filtered WHERE id>$9 ORDER BY id LIMIT $8 OFFSET $7)
 SELECT (SELECT registered FROM connections), jsonb_build_object(
 'items',coalesce((SELECT jsonb_agg(to_jsonb(page) ORDER BY id) FROM page),'[]'::jsonb),
 'counts',jsonb_build_object('total',(SELECT count(*) FROM facts),'mapped',(SELECT count(*) FROM facts WHERE state='mapped'),'unmapped',(SELECT count(*) FROM facts WHERE state='unmapped'),'attention',(SELECT count(*) FROM facts WHERE state NOT IN ('mapped','unmapped'))),
 'total',(SELECT count(*) FROM filtered),'has_next',(SELECT count(*) FROM filtered WHERE id>$9)>$7+$8,'offset',$7,
 'ozon_imported',EXISTS(SELECT 1 FROM oi),'moy_imported',EXISTS(SELECT 1 FROM mi))`
