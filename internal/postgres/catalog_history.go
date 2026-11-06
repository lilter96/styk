package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

// Filters apply before cursor pagination and to each work row, including mixed
// operations. They never widen the authenticated account pair.
const catalogHistoryFilter = `
 ($7='' OR strpos(lower(o.id),lower($7))>0
 OR strpos(lower(COALESCE(w.work#>>'{decision,source,fields,name,data}','')),lower($7))>0
 OR strpos(lower(COALESCE(w.work#>>'{decision,source,identity,offer_id}','')),lower($7))>0
 OR strpos(lower(COALESCE(w.work#>>'{decision,source,fields,article,data}','')),lower($7))>0
 OR strpos(lower(COALESCE(w.work#>>'{command,target,offer_id}','')),lower($7))>0)
 AND ($8='' OR w.state=$8 OR ($8='active' AND w.state IN ('QUEUED','CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED')))
 AND ($10='' OR (w.work#>>'{decision,source,identity,account,provider}'=$9 AND w.work#>>'{decision,source,identity,id}'=$10)
 OR (w.work#>>'{command,target,account,provider}'=$9 AND w.work#>>'{command,target,id}'=$10)
 OR (w.work#>>'{created_target,account,provider}'=$9 AND w.work#>>'{created_target,id}'=$10))`

func validCatalogHistoryPage(p catalogsync.PageRequest) bool {
	if p.Limit < 1 || p.Limit > 100 || len(p.Search) > 512 || len(p.ProductID) > 512 || strings.ContainsRune(p.Search+p.ProductID, 0) {
		return false
	}
	if (p.ProductID == "") != (p.ProductProvider == "") || !validCatalogPageProvider(p.ProductProvider) {
		return false
	}
	switch p.State {
	case "", "active", "QUEUED", "CLAIMED", "DISPATCHING", "PROCESSING", "ACKNOWLEDGED", "UNKNOWN", "RECONCILED", "REJECTED", "BLOCKED":
		return true
	default:
		return false
	}
}

func catalogHistoryCursor(scope catalog.Scope, page catalogsync.PageRequest) (catalogCursor, error) {
	// Keep product cursors independent; history cursors bind every filter.
	binding, err := catalogHash(struct{ Search, State, Provider, Product string }{page.Search, page.State, string(page.ProductProvider), page.ProductID})
	if err != nil {
		return catalogCursor{}, err
	}
	page.Search = binding
	return decodeCatalogCursor(scope, page)
}

func (s *Store) CatalogHistory(ctx context.Context, scope catalog.Scope, page catalogsync.PageRequest) (catalogsync.HistoryPage, error) {
	result := catalogsync.HistoryPage{Operations: make([]catalogsync.Operation, 0)}
	if scope.Validate() != nil || !validCatalogHistoryPage(page) {
		return result, catalogsync.ErrInvalid
	}
	cursor, err := catalogHistoryCursor(scope, page)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, fmt.Errorf("begin catalog history: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	args := append(catalogScopeArgs(scope), cursor.ID, cursor.At, page.Limit+1, page.Search, page.State, string(page.ProductProvider), page.ProductID)
	result, err = readFilteredCatalogHistory(ctx, tx, scope, page, cursor, args)
	if err != nil {
		return result, err
	}
	result.Summary, err = catalogHistorySummary(ctx, tx, args)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit catalog history: %w", err)
	}
	return result, nil
}

func readFilteredCatalogHistory(ctx context.Context, tx pgx.Tx, scope catalog.Scope, page catalogsync.PageRequest, cursor catalogCursor, args []any) (catalogsync.HistoryPage, error) {
	result := catalogsync.HistoryPage{Operations: make([]catalogsync.Operation, 0, page.Limit)}
	query := `WITH selected AS (SELECT o.* FROM catalog_sync_operations o WHERE o.tenant_id=$1 AND o.moysklad_account=$2 AND o.ozon_account=$3 AND ($4='' OR (o.created_at,o.id)<($5,$4)) AND EXISTS(SELECT 1 FROM catalog_sync_work w WHERE w.operation_id=o.id AND ` + catalogHistoryFilter + `) ORDER BY o.created_at DESC,o.id DESC LIMIT $6)
 SELECT o.id,o.preview_id,o.created_at,COALESCE(jsonb_agg(w.work || jsonb_build_object('mapping_state',COALESCE(l.state,'')) ORDER BY w.id),'[]'::jsonb) FROM selected o JOIN catalog_sync_work w ON w.operation_id=o.id LEFT JOIN catalog_created_links l ON l.work_id=w.id WHERE ` + catalogHistoryFilter + ` GROUP BY o.id,o.preview_id,o.created_at ORDER BY o.created_at DESC,o.id DESC`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("list filtered catalog history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		op, err := scanCatalogOperation(rows, scope)
		if err != nil {
			return result, err
		}
		if len(result.Operations) == page.Limit {
			result.NextCursor, err = encodeCatalogCursor(cursor)
			if err != nil {
				return result, err
			}
			break
		}
		result.Operations = append(result.Operations, op)
		cursor.ID, cursor.At = op.ID, op.CreatedAt
	}
	if err = rows.Err(); err != nil {
		return result, fmt.Errorf("read filtered catalog history: %w", err)
	}
	return result, nil
}

func catalogHistorySummary(ctx context.Context, tx pgx.Tx, args []any) (map[string]int64, error) {
	values := append([]any(nil), args...)
	values[7] = ""
	// Unused pagination arguments remain typed so PostgreSQL can bind the same
	// parameter positions as the paginated query.
	query := `SELECT w.state,count(*) FROM catalog_sync_operations o JOIN catalog_sync_work w ON w.operation_id=o.id WHERE o.tenant_id=$1 AND o.moysklad_account=$2 AND o.ozon_account=$3 AND $4::text IS NOT NULL AND $5::timestamptz IS NOT NULL AND $6::int>0 AND ` + catalogHistoryFilter + ` GROUP BY w.state`
	rows, err := tx.Query(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("summarize catalog history: %w", err)
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var state string
		var count int64
		if err = rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("read catalog history count: %w", err)
		}
		result[state] = count
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read catalog history summary: %w", err)
	}
	return result, nil
}
