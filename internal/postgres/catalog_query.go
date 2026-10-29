package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

type catalogCursor struct {
	Binding        string    `json:"binding"`
	MoyskladImport int64     `json:"m"`
	OzonImport     int64     `json:"o"`
	Provider       string    `json:"p"`
	Kind           string    `json:"k"`
	ID             string    `json:"id"`
	At             time.Time `json:"at,omitempty"`
}

func catalogPageBinding(scope catalog.Scope, search string, provider catalog.Provider) (string, error) {
	return catalogHash(struct {
		Scope    catalog.Scope
		Search   string
		Provider catalog.Provider
	}{scope, search, provider})
}

func decodeCatalogCursor(scope catalog.Scope, page catalogsync.PageRequest) (catalogCursor, error) {
	var cursor catalogCursor
	binding, err := catalogPageBinding(scope, page.Search, page.Provider)
	if err != nil {
		return cursor, err
	}
	cursor.Binding = binding
	if page.Cursor == "" {
		return cursor, nil
	}
	if len(page.Cursor) > 4096 {
		return cursor, catalogsync.ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(page.Cursor)
	if err != nil {
		return cursor, catalogsync.ErrInvalid
	}
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.Binding != binding {
		return cursor, catalogsync.ErrInvalid
	}
	return cursor, nil
}

func encodeCatalogCursor(cursor catalogCursor) (string, error) {
	raw, err := catalogJSON(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Store) CatalogProducts(ctx context.Context, scope catalog.Scope, page catalogsync.PageRequest) (catalogsync.ProductPage, error) {
	var result catalogsync.ProductPage
	if scope.Validate() != nil || page.Limit < 1 || page.Limit > 100 || len(page.Search) > 512 || !validCatalogPageProvider(page.Provider) {
		return result, catalogsync.ErrInvalid
	}
	cursor, err := decodeCatalogCursor(scope, page)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, fmt.Errorf("begin catalog selection: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if page.Cursor == "" {
		if err = pinCatalogImports(ctx, tx, scope, &cursor); err != nil {
			return result, err
		}
	}
	result, err = readCatalogProducts(ctx, tx, scope, page, cursor)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit catalog selection: %w", err)
	}
	return result, nil
}

func pinCatalogImports(ctx context.Context, q catalogQuerier, scope catalog.Scope, cursor *catalogCursor) error {
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT i.id FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1),0),COALESCE((SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='ozon' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1),0)`, catalogScopeArgs(scope)...).Scan(&cursor.MoyskladImport, &cursor.OzonImport)
	if err != nil {
		return fmt.Errorf("pin catalog product imports: %w", err)
	}
	return nil
}

const catalogProductsSQL = `WITH moy AS (
 SELECT 'moysklad'::text provider,e.kind,e.external_id id,''::text offer_id,''::text sku,e.name,e.article,e.parent_id,NULL::boolean archived,i.finished_at observed_at,i.fingerprint||':'||e.source_updated revision
 FROM source_entities e JOIN source_imports i ON i.id=e.import_id JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 WHERE ($11='' OR $11='moysklad') AND e.import_id=$4 AND i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) AND e.kind IN ('product','variant')
 AND ('moysklad',e.kind,e.external_id)>($6,$7,$8) AND ($9='' OR e.external_id ILIKE '%'||$9||'%' OR e.name ILIKE '%'||$9||'%' OR e.article ILIKE '%'||$9||'%' OR e.code ILIKE '%'||$9||'%') ORDER BY e.kind,e.external_id LIMIT $10
), ozon AS (
 SELECT 'ozon'::text provider,'product'::text kind,p.product_id id,p.offer_id,COALESCE(p.sku,'') sku,p.name,''::text article,''::text parent_id,p.archived,i.finished_at observed_at,i.fingerprint revision
 FROM ozon_product_observations p JOIN source_imports i ON i.id=p.import_id JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 WHERE ($11='' OR $11='ozon') AND p.import_id=$5 AND i.source='ozon' AND i.account_id=$3 AND (c.include_import_history OR i.started_at>=c.registered_at)
 AND ('ozon','product',p.product_id)>($6,$7,$8) AND ($9='' OR p.product_id ILIKE '%'||$9||'%' OR p.offer_id ILIKE '%'||$9||'%' OR p.name ILIKE '%'||$9||'%') ORDER BY p.product_id LIMIT $10
) SELECT * FROM (SELECT * FROM moy UNION ALL SELECT * FROM ozon) products ORDER BY provider,kind,id LIMIT $10`

func readCatalogProducts(ctx context.Context, q catalogQuerier, scope catalog.Scope, page catalogsync.PageRequest, cursor catalogCursor) (catalogsync.ProductPage, error) {
	result := catalogsync.ProductPage{Products: make([]catalog.Product, 0, page.Limit)}
	args := append(catalogScopeArgs(scope), cursor.MoyskladImport, cursor.OzonImport, cursor.Provider, cursor.Kind, cursor.ID, page.Search, page.Limit+1, page.Provider)
	rows, err := q.Query(ctx, catalogProductsSQL, args...)
	if err != nil {
		return result, fmt.Errorf("select catalog product page: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanCatalogProduct(rows, scope)
		if err != nil {
			return result, err
		}
		if len(result.Products) == page.Limit {
			result.NextCursor, err = encodeCatalogCursor(cursor)
			if err != nil {
				return result, err
			}
			break
		}
		result.Products = append(result.Products, p)
		cursor.Provider = string(p.Identity.Account.Provider)
		cursor.Kind = p.Identity.EntityType
		cursor.ID = p.Identity.ID
	}
	if err = rows.Err(); err != nil {
		return result, fmt.Errorf("read catalog product page: %w", err)
	}
	return result, nil
}

func scanCatalogProduct(row pgx.Row, scope catalog.Scope) (catalog.Product, error) {
	p := catalog.Product{Lifecycle: catalog.LifecycleUnknown, Fields: make(map[catalog.Field]catalog.Value, 3)}
	var name *string
	var article, parent string
	var archived *bool
	err := row.Scan(&p.Identity.Account.Provider, &p.Identity.EntityType, &p.Identity.ID, &p.Identity.OfferID, &p.Identity.SKU, &name, &article, &parent, &archived, &p.ObservedAt, &p.Revision)
	if err != nil {
		return p, fmt.Errorf("scan catalog product selection: %w", err)
	}
	p.Identity.Account.ID = scope.OzonAccount
	if p.Identity.Account.Provider == catalog.ProviderMoySklad {
		p.Identity.Account.ID = scope.MoyskladAccount
	}
	p.Fields[catalog.FieldName] = catalog.Value{State: catalog.StateUnknown}
	if name != nil {
		p.Fields[catalog.FieldName] = catalog.Text(*name)
	}
	p.Fields[catalog.FieldDescription] = catalog.Value{State: catalog.StateUnknown}
	if p.Identity.Account.Provider == catalog.ProviderMoySklad {
		p.Fields[catalog.FieldArticle] = catalog.Text(article)
	}
	p.Variant = p.Identity.EntityType == "variant"
	if parent != "" {
		p.Parent = &catalog.Identity{Account: p.Identity.Account, EntityType: "product", ID: parent}
	}
	if archived != nil {
		p.Lifecycle = catalog.LifecycleActive
		if *archived {
			p.Lifecycle = catalog.LifecycleArchived
		}
	}
	return p, nil
}

func (s *Store) CatalogHistory(ctx context.Context, scope catalog.Scope, page catalogsync.PageRequest) (catalogsync.HistoryPage, error) {
	result := catalogsync.HistoryPage{Operations: make([]catalogsync.Operation, 0)}
	if scope.Validate() != nil || page.Limit < 1 || page.Limit > 100 || page.Search != "" {
		return result, catalogsync.ErrInvalid
	}
	cursor, err := decodeCatalogCursor(scope, page)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	args := append(catalogScopeArgs(scope), cursor.ID, cursor.At, page.Limit+1)
	rows, err := s.Pool.Query(ctx, `SELECT o.id,o.preview_id,o.created_at,COALESCE(jsonb_agg(w.work ORDER BY w.id) FILTER(WHERE w.id IS NOT NULL),'[]'::jsonb) FROM (SELECT * FROM catalog_sync_operations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND ($4='' OR (created_at,id)<($5,$4)) ORDER BY created_at DESC,id DESC LIMIT $6) o LEFT JOIN catalog_sync_work w ON w.operation_id=o.id GROUP BY o.id,o.preview_id,o.created_at ORDER BY o.created_at DESC,o.id DESC`, args...)
	if err != nil {
		return result, fmt.Errorf("list catalog operation history: %w", err)
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
		cursor.ID = op.ID
		cursor.At = op.CreatedAt
	}
	if err = rows.Err(); err != nil {
		return result, fmt.Errorf("read catalog history: %w", err)
	}
	return result, nil
}

func readCatalogOperation(ctx context.Context, q catalogQuerier, scope catalog.Scope, id string) (catalogsync.Operation, error) {
	args := append(catalogScopeArgs(scope), id)
	return scanCatalogOperation(q.QueryRow(ctx, `SELECT o.id,o.preview_id,o.created_at,COALESCE(jsonb_agg(w.work ORDER BY w.id) FILTER(WHERE w.id IS NOT NULL),'[]'::jsonb) FROM catalog_sync_operations o LEFT JOIN catalog_sync_work w ON w.operation_id=o.id WHERE o.tenant_id=$1 AND o.moysklad_account=$2 AND o.ozon_account=$3 AND o.id=$4 GROUP BY o.id,o.preview_id,o.created_at`, args...), scope)
}

func scanCatalogOperation(row pgx.Row, scope catalog.Scope) (catalogsync.Operation, error) {
	op := catalogsync.Operation{Scope: scope, State: catalogsync.StateReconciled}
	var raw []byte
	if err := row.Scan(&op.ID, &op.PreviewID, &op.CreatedAt, &raw); err != nil {
		return op, catalogError("read catalog operation", err)
	}
	if err := catalogDecode(raw, &op.Targets); err != nil {
		return op, err
	}
	for _, work := range op.Targets {
		if work.State != catalogsync.StateReconciled {
			op.State = work.State
			break
		}
	}
	return op, nil
}

func validCatalogPageProvider(provider catalog.Provider) bool {
	return provider == "" || provider == catalog.ProviderMoySklad || provider == catalog.ProviderOzon
}
