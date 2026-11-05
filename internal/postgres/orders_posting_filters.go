package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// Filters are evaluated before the cursor limit, across the scoped collection.
// Attention describes local evidence and outcomes, not a new provider read.
const filteredOrderPostingsSQL = `SELECT h.projection,h.checked_at,h.quarantined
 FROM orders_posting_heads h
 JOIN orders_posting_observations o ON o.id=h.observation_id AND o.tenant_id=h.tenant_id
 AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number
 LEFT JOIN LATERAL (SELECT w.state,w.work#>>'{command,expected_source_revision}' AS source_revision
 FROM orders_work w WHERE w.tenant_id=h.tenant_id AND w.moysklad_account=h.moysklad_account
 AND w.ozon_account=h.ozon_account AND w.posting_number=h.posting_number
 ORDER BY w.created_at DESC,w.id DESC LIMIT 1) latest ON $8
 WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number>$4
 AND ($5='' OR h.posting_number ILIKE '%'||$5||'%' OR o.commercial_order_id ILIKE '%'||$5||'%'
 OR (h.projection#>>'{order_number,presence}' IN ('value','zero') AND h.projection#>>'{order_number,value}' ILIKE '%'||$5||'%'))
 AND ($7='' OR (h.projection#>>'{status,presence}' IN ('value','zero') AND h.projection#>>'{status,value}'=$7))
 AND (NOT $8 OR h.quarantined OR jsonb_array_length(COALESCE(NULLIF(h.projection->'drift','null'::jsonb),'[]'::jsonb))>0
 OR latest.state IN ('UNKNOWN','BLOCKED','REJECTED')
 OR (latest.state IS NOT NULL AND latest.source_revision IS DISTINCT FROM h.projection#>>'{evidence,revision}')
 OR EXISTS (SELECT 1 FROM orders_target_fences f JOIN orders_work w ON w.id=f.work_id AND w.tenant_id=f.tenant_id
 AND w.moysklad_account=f.moysklad_account AND w.ozon_account=f.ozon_account
 WHERE f.tenant_id=h.tenant_id AND f.moysklad_account=h.moysklad_account AND f.ozon_account=h.ozon_account
 AND f.posting_number=h.posting_number AND w.state='UNKNOWN'))
 ORDER BY h.posting_number LIMIT $6`

func readFilteredOrderPostings(ctx context.Context, q ordersQuerier, scope orders.Scope, after string, page orders.PageRequest) ([]orders.Posting, error) {
	if page.Status == "" && !page.Attention {
		return readOrderPostings(ctx, q, scope, after, page.Search, page.Limit+1)
	}
	args := append(ordersScopeArgs(scope), after, page.Search, page.Limit+1, page.Status, page.Attention)
	rows, err := q.Query(ctx, filteredOrderPostingsSQL, args...)
	if err != nil {
		return nil, ordersError("select filtered order postings", err)
	}
	defer rows.Close()
	result := make([]orders.Posting, 0, page.Limit+1)
	for rows.Next() {
		posting, scanErr := scanOrderPosting(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, posting)
	}
	return result, ordersRowsError("read filtered order postings", rows.Err())
}
