//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

func catalogSyncSelectionData(t testing.TB, count int) (*postgres.Store, catalog.Scope) {
	t.Helper()
	db := catalogSyncDB(t)
	scope := catalog.Scope{Tenant: "catalog-selection", MoyskladAccount: "selection-source", OzonAccount: "selection-market"}
	catalogSyncConnections(t, db, scope)
	ctx := context.Background()
	var moy, ozon int64
	for _, source := range []struct {
		provider, account string
		id                *int64
	}{{"moysklad", scope.MoyskladAccount, &moy}, {"ozon", scope.OzonAccount, &ozon}} {
		if err := db.Pool.QueryRow(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES($1,$2,'selection-revision',clock_timestamp(),clock_timestamp(),'catalog-test') RETURNING id`, source.provider, source.account).Scan(source.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO source_entities(import_id,kind,external_id,name,code,article,parent_id,source_updated) SELECT $1,'product','source-'||lpad(n::text,6,'0'),'Product '||lpad(n::text,6,'0'),'code-'||n,'article-'||n,'','revision-1' FROM generate_series(1,$2::integer) n`, moy, count/2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO ozon_import_batches(import_id,traversal_status) VALUES($1,'completed')`, ozon); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO ozon_product_observations(import_id,product_id,offer_id,name,archived) SELECT $1,'market-'||lpad(n::text,6,'0'),'offer-'||n,'Market '||lpad(n::text,6,'0'),false FROM generate_series(1,$2::integer) n`, ozon, count-count/2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `ANALYZE source_entities; ANALYZE ozon_product_observations; ANALYZE source_imports; ANALYZE stock_connections`); err != nil {
		t.Fatal(err)
	}
	return db, scope
}

func TestCatalogSyncProductSelectionKeysetBoundedAndTenantSafe(t *testing.T) {
	db, scope := catalogSyncSelectionData(t, 1000)
	ctx := context.Background()
	page := catalogsync.PageRequest{Limit: 73}
	seen := make(map[string]bool, 1000)
	for {
		result, err := db.CatalogProducts(ctx, scope, page)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Products) > page.Limit {
			t.Fatal("unbounded product result")
		}
		for _, product := range result.Products {
			id := string(product.Identity.Account.Provider) + product.Identity.ID
			if seen[id] {
				t.Fatal("duplicate keyset product")
			}
			seen[id] = true
			if product.Fields[catalog.FieldDescription].State != catalog.StateUnknown {
				t.Fatal("missing description was fabricated")
			}
		}
		if result.NextCursor == "" {
			break
		}
		page.Cursor = result.NextCursor
	}
	if len(seen) != 1000 {
		t.Fatalf("catalog traversal lost rows: %d", len(seen))
	}
	catalogSyncAssertSelectionIsolation(t, db, scope)

}

func TestCatalogSyncProductCursorPinsImportRevision(t *testing.T) {
	db, scope := catalogSyncSelectionData(t, 100)
	ctx := context.Background()
	first, err := db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('moysklad',$1,'empty-new-import',clock_timestamp(),clock_timestamp(),'catalog-test')`, scope.MoyskladAccount); err != nil {
		t.Fatal(err)
	}
	second, err := db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Products) != 1 || second.Products[0].Identity.ID != "source-000002" {
		t.Fatalf("cursor followed newer import: %+v %v", second, err)
	}
}

func BenchmarkCatalogSyncProductSelection(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("products_%d", count), func(b *testing.B) {
			db, scope := catalogSyncSelectionData(b, count)
			ctx := context.Background()
			page := catalogsync.PageRequest{Limit: 50}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result, err := db.CatalogProducts(ctx, scope, page)
				if err != nil || len(result.Products) != 50 {
					b.Fatalf("bounded catalog page: %d %v", len(result.Products), err)
				}
			}
			b.StopTimer()
			b.ReportMetric(50, "products/op")
		})
	}
}

func TestCatalogSyncProviderSelectionAndCursorBinding(t *testing.T) {
	db, scope := catalogSyncSelectionData(t, 100)
	ctx := context.Background()
	tests := []struct {
		name     string
		provider catalog.Provider
	}{{"moysklad", catalog.ProviderMoySklad}, {"ozon", catalog.ProviderOzon}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 100, Provider: tc.provider})
			if err != nil || len(page.Products) != 50 {
				t.Fatalf("provider page: %d %v", len(page.Products), err)
			}
			for _, product := range page.Products {
				if product.Identity.Account.Provider != tc.provider {
					t.Fatal("provider selection leaked opposite side")
				}
			}
		})
	}
	page, err := db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1, Provider: catalog.ProviderMoySklad})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1, Provider: catalog.ProviderOzon, Cursor: page.NextCursor}); err == nil {
		t.Fatal("provider substitution reused cursor")
	}
	if _, err = db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1, Provider: "unregistered"}); err == nil {
		t.Fatal("unsupported provider accepted")
	}
}

func TestCatalogSyncInitialConfigRequiresOwnedPair(t *testing.T) {
	db, scope := catalogSyncSelectionData(t, 100)
	config, err := db.CatalogConfig(context.Background(), scope)
	if err != nil || config.Revision != 0 || config.Enabled || config.TestEnabled || config.Scope != scope {
		t.Fatalf("readonly setup: %+v %v", config, err)
	}
	if _, err = db.CatalogHistory(context.Background(), scope, catalogsync.PageRequest{Limit: -1}); err == nil {
		t.Fatal("invalid page limit accepted")
	}
}

func catalogSyncAssertSelectionIsolation(t *testing.T, db *postgres.Store, scope catalog.Scope) {
	t.Helper()
	ctx := context.Background()
	first, err := db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	foreign := scope
	foreign.Tenant = "other-tenant"
	if _, err = db.CatalogProducts(ctx, foreign, catalogsync.PageRequest{Limit: 1, Cursor: first.NextCursor}); err == nil {
		t.Fatal("foreign tenant reused cursor")
	}
	if _, err = db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 1, Cursor: first.NextCursor, Search: "changed"}); err == nil {
		t.Fatal("search substitution reused cursor")
	}
	empty, err := db.CatalogProducts(ctx, foreign, catalogsync.PageRequest{Limit: 10})
	if err != nil || len(empty.Products) != 0 {
		t.Fatalf("foreign selection leaked products: %d %v", len(empty.Products), err)
	}
	search, err := db.CatalogProducts(ctx, scope, catalogsync.PageRequest{Limit: 100, Search: "Product 000500"})
	if err != nil || len(search.Products) != 1 || search.Products[0].Identity.ID != "source-000500" {
		t.Fatalf("bounded search: %+v %v", search, err)
	}
}
