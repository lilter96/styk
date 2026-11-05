package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/lilter96/styk/internal/orders"
)

const orderTargetStatesSQL = `WITH selected AS (SELECT unnest($4::text[]) AS number),
 latest AS (SELECT DISTINCT ON(w.posting_number) w.posting_number,w.id,w.state,w.work#>>'{command,expected_source_revision}' AS source_revision
 FROM orders_work w JOIN selected s ON s.number=w.posting_number
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 ORDER BY w.posting_number,w.created_at DESC,w.id DESC),
 unresolved AS (SELECT DISTINCT f.posting_number FROM orders_target_fences f
 JOIN orders_work w ON w.id=f.work_id AND w.tenant_id=f.tenant_id
 AND w.moysklad_account=f.moysklad_account AND w.ozon_account=f.ozon_account
 JOIN selected s ON s.number=f.posting_number
 WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND w.state='UNKNOWN')
 SELECT s.number,w.id IS NOT NULL,COALESCE(w.state,''),u.posting_number IS NOT NULL,
 COALESCE(b.customer_order_id,''),CASE WHEN w.id IS NULL THEN false ELSE w.source_revision IS DISTINCT FROM h.projection#>>'{evidence,revision}' END,clock_timestamp()
 FROM selected s JOIN orders_posting_heads h ON h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number=s.number
 LEFT JOIN latest w ON w.posting_number=s.number
 LEFT JOIN unresolved u ON u.posting_number=s.number
 LEFT JOIN orders_projection_bindings b ON b.tenant_id=$1 AND b.moysklad_account=$2
 AND b.ozon_account=$3 AND b.posting_number=s.number ORDER BY s.number`

func readOrderTargetStates(ctx context.Context, q ordersQuerier, scope orders.Scope, postings []orders.Posting) ([]orders.PostingTargetState, error) {
	if len(postings) == 0 {
		return nil, nil
	}
	if len(postings) > 100 || scope.Validate() != nil {
		return nil, orders.ErrInvalid
	}
	numbers := make([]string, len(postings))
	for i, p := range postings {
		if p.Scope != scope || p.PostingNumber == "" {
			return nil, orders.ErrStale
		}
		numbers[i] = p.PostingNumber
	}
	rows, err := q.Query(ctx, orderTargetStatesSQL, append(ordersScopeArgs(scope), numbers)...)
	if err != nil {
		return nil, ordersError("read current posting target states", err)
	}
	return scanOrderTargetStates(rows, scope, len(postings))
}

func scanOrderTargetStates(rows pgx.Rows, scope orders.Scope, expected int) ([]orders.PostingTargetState, error) {
	defer rows.Close()
	result := make([]orders.PostingTargetState, 0, expected)
	for rows.Next() {
		item := orders.PostingTargetState{Scope: scope}
		if err := rows.Scan(&item.PostingNumber, &item.HasOperation, &item.State, &item.Unresolved, &item.CustomerOrderID, &item.SourceChanged, &item.CheckedAt); err != nil {
			return nil, ordersError("decode current posting target state", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, ordersRowsError("finish current posting target states", err)
	}
	if len(result) != expected {
		return nil, orders.ErrStale
	}
	return result, nil
}
