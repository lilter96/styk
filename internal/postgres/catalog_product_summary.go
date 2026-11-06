package postgres

import (
	"context"
	"fmt"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

// Aggregate the entire pinned catalog, never the currently visible page. These
// counts describe selection eligibility; provider preflight remains authoritative.
func catalogProductSummary(ctx context.Context, q catalogQuerier, scope catalog.Scope, page catalogsync.PageRequest, cursor catalogCursor) (*catalogsync.ProductSummary, error) {
	if page.Provider == "" {
		return nil, nil
	}
	v := new(catalogsync.ProductSummary)
	args := append(catalogScopeArgs(scope), cursor.MoyskladImport, cursor.OzonImport, page.Provider, page.Search)
	err := q.QueryRow(ctx, catalogProductSummarySQL, args...).Scan(&v.Total, &v.Linked, &v.Excluded, &v.ToReview, &v.ObservedAt)
	if err != nil {
		return nil, fmt.Errorf("read catalog import summary: %w", err)
	}
	return v, nil
}

const catalogProductSummarySQL = `WITH products AS (
 SELECT 'moysklad' provider,e.kind,e.external_id id,e.archived,i.finished_at
 FROM source_entities e JOIN source_imports i ON i.id=e.import_id
 JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 WHERE $6='moysklad' AND i.id=$4 AND i.account_id=$2 AND i.source='moysklad' AND e.kind IN ('product','variant')
 AND (c.include_import_history OR i.started_at>=c.registered_at)
 AND ($7='' OR e.external_id ILIKE '%'||$7||'%' OR e.name ILIKE '%'||$7||'%' OR e.article ILIKE '%'||$7||'%' OR e.code ILIKE '%'||$7||'%')
 UNION ALL
 SELECT 'ozon','product',p.product_id,p.archived,i.finished_at
 FROM ozon_product_observations p JOIN source_imports i ON i.id=p.import_id
 JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed'
 WHERE $6='ozon' AND i.id=$5 AND i.account_id=$3 AND i.source='ozon' AND (c.include_import_history OR i.started_at>=c.registered_at)
 AND ($7='' OR p.product_id ILIKE '%'||$7||'%' OR p.offer_id ILIKE '%'||$7||'%' OR p.name ILIKE '%'||$7||'%')
), classified AS (
 SELECT p.*,EXISTS(SELECT 1 FROM product_mappings m WHERE m.tenant_id=$1 AND m.ozon_account=$3
 AND ((p.provider='ozon' AND m.ozon_product_id=p.id) OR (p.provider='moysklad' AND m.moysklad_account=$2 AND m.target_kind=p.kind AND m.target_id=p.id))) linked
 FROM products p)
 SELECT count(*),count(*) FILTER(WHERE linked),count(*) FILTER(WHERE NOT linked AND (archived IS DISTINCT FROM false OR kind='variant')),
 count(*) FILTER(WHERE NOT linked AND archived=false AND kind='product'),max(finished_at) FROM classified`
