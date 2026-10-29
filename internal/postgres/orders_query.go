package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type ordersCursor struct {
	Binding string    `json:"binding"`
	ID      string    `json:"id"`
	At      time.Time `json:"at,omitempty"`
}

func orderPageCursor(scope orders.Scope, page orders.PageRequest, kind string) (ordersCursor, error) {
	var cursor ordersCursor
	if scope.Validate() != nil || page.Limit < 1 || page.Limit > 100 || len(page.Search) > 256 || len(page.Cursor) > 2048 {
		return cursor, orders.ErrInvalid
	}
	binding, err := ordersHash(struct {
		Scope  orders.Scope
		Search string
		Kind   string
	}{scope, page.Search, kind})
	if err != nil {
		return cursor, err
	}
	if page.Cursor == "" {
		return ordersCursor{Binding: binding}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(page.Cursor)
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Binding != binding {
		return cursor, orders.ErrInvalid
	}
	return cursor, nil
}

func encodeOrdersCursor(cursor ordersCursor) (string, error) {
	raw, err := ordersJSON(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Store) OrderPostings(ctx context.Context, scope orders.Scope, page orders.PageRequest) (orders.PostingPage, error) {
	result := orders.PostingPage{Postings: []orders.Posting{}}
	cursor, err := orderPageCursor(scope, page, "postings")
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	postings, err := readOrderPostings(ctx, s.Pool, scope, cursor.ID, page.Search, page.Limit+1)
	if err != nil {
		return result, err
	}
	if len(postings) > page.Limit {
		postings = postings[:page.Limit]
		cursor.ID = postings[len(postings)-1].PostingNumber
		result.NextCursor, err = encodeOrdersCursor(cursor)
		if err != nil {
			return result, err
		}
	}
	result.Postings = postings
	return result, nil
}

// ActiveOrderPostings refreshes old active postings independently of their
// original creation window. after is an opaque provider posting identifier.
func (s *Store) ActiveOrderPostings(ctx context.Context, scope orders.Scope, limit int, after string) (orders.PostingPage, error) {
	result := orders.PostingPage{Postings: []orders.Posting{}}
	if scope.Validate() != nil || limit < 1 || limit > 100 || len(after) > 256 {
		return result, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	args := append(ordersScopeArgs(scope), after, limit+1)
	rows, err := s.Pool.Query(ctx, `SELECT h.projection,h.checked_at,h.quarantined FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.active AND h.posting_number>$4 ORDER BY h.posting_number LIMIT $5`, args...)
	if err != nil {
		return result, ordersError("select active order postings", err)
	}
	defer rows.Close()
	for rows.Next() {
		posting, scanErr := scanOrderPosting(rows)
		if scanErr != nil {
			return result, scanErr
		}
		if len(result.Postings) == limit {
			result.NextCursor = result.Postings[len(result.Postings)-1].PostingNumber
			break
		}
		result.Postings = append(result.Postings, posting)
	}
	return result, ordersRowsError("read active order postings", rows.Err())
}

func readOrderPostings(ctx context.Context, q ordersQuerier, scope orders.Scope, after, search string, limit int) ([]orders.Posting, error) {
	args := append(ordersScopeArgs(scope), after, search, limit)
	rows, err := q.Query(ctx, `SELECT h.projection,h.checked_at,h.quarantined FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number>$4 AND ($5='' OR h.posting_number ILIKE '%'||$5||'%' OR o.commercial_order_id ILIKE '%'||$5||'%') ORDER BY h.posting_number LIMIT $6`, args...)
	if err != nil {
		return nil, ordersError("select order posting page", err)
	}
	defer rows.Close()
	result := make([]orders.Posting, 0, min(limit, 1000))
	for rows.Next() {
		posting, scanErr := scanOrderPosting(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, posting)
	}
	return result, ordersRowsError("read order posting page", rows.Err())
}

func scanOrderPosting(row pgx.Row) (orders.Posting, error) {
	var posting orders.Posting
	var raw []byte
	var checked time.Time
	var quarantined bool
	if err := row.Scan(&raw, &checked, &quarantined); err != nil {
		return posting, ordersError("scan order posting", err)
	}
	if err := ordersDecode(raw, &posting); err != nil {
		return posting, err
	}
	posting.Evidence.CheckedAt = checked
	if quarantined {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "evidence", Code: "ORDERING_SUPERSEDED_OBSERVATION"})
	}
	return posting, nil
}

func (s *Store) OrderHistory(ctx context.Context, scope orders.Scope, page orders.PageRequest) (orders.HistoryPage, error) {
	result := orders.HistoryPage{Operations: []orders.Operation{}}
	cursor, err := orderPageCursor(scope, page, "history")
	if err != nil || page.Search != "" {
		return result, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	args := append(ordersScopeArgs(scope), cursor.ID, cursor.At, page.Limit+1)
	rows, err := s.Pool.Query(ctx, `SELECT o.id,o.preview_id,o.created_at,COALESCE(jsonb_agg(w.work ORDER BY w.id) FILTER(WHERE w.id IS NOT NULL),'[]'::jsonb) FROM (SELECT * FROM orders_operations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND ($4='' OR (created_at,id)<($5,$4)) ORDER BY created_at DESC,id DESC LIMIT $6) o LEFT JOIN orders_work w ON w.operation_id=o.id GROUP BY o.id,o.preview_id,o.created_at ORDER BY o.created_at DESC,o.id DESC`, args...)
	if err != nil {
		return result, ordersError("select order history", err)
	}
	return scanOrderHistory(rows, scope, page, cursor)
}

func scanOrderHistory(rows pgx.Rows, scope orders.Scope, page orders.PageRequest, cursor ordersCursor) (orders.HistoryPage, error) {
	defer rows.Close()
	result := orders.HistoryPage{Operations: []orders.Operation{}}
	for rows.Next() {
		operation, err := scanOrderOperation(rows, scope)
		if err != nil {
			return result, err
		}
		if len(result.Operations) == page.Limit {
			result.NextCursor, err = encodeOrdersCursor(cursor)
			return result, err
		}
		result.Operations = append(result.Operations, operation)
		cursor.ID, cursor.At = operation.ID, operation.CreatedAt
	}
	return result, ordersRowsError("read order history", rows.Err())
}

func readOrderOperation(ctx context.Context, q ordersQuerier, scope orders.Scope, id string) (orders.Operation, error) {
	args := append(ordersScopeArgs(scope), id)
	return scanOrderOperation(q.QueryRow(ctx, `SELECT o.id,o.preview_id,o.created_at,COALESCE(jsonb_agg(w.work ORDER BY w.id) FILTER(WHERE w.id IS NOT NULL),'[]'::jsonb) FROM orders_operations o LEFT JOIN orders_work w ON w.operation_id=o.id WHERE o.tenant_id=$1 AND o.moysklad_account=$2 AND o.ozon_account=$3 AND o.id=$4 GROUP BY o.id,o.preview_id,o.created_at`, args...), scope)
}

func scanOrderOperation(row pgx.Row, scope orders.Scope) (orders.Operation, error) {
	operation := orders.Operation{Scope: scope, State: orders.StateReconciled}
	var raw []byte
	if err := row.Scan(&operation.ID, &operation.PreviewID, &operation.CreatedAt, &raw); err != nil {
		return operation, ordersError("read order operation", err)
	}
	if err := ordersDecode(raw, &operation.Targets); err != nil {
		return operation, err
	}
	for _, work := range operation.Targets {
		if work.State != orders.StateReconciled {
			operation.State = work.State
			break
		}
	}
	return operation, nil
}
