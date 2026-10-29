//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/postgres"
)

// BenchmarkCatalogProducts creates an isolated schema, applies the real
// migrations, and exercises an authenticated page with 100, 1,000, or 10,000
// synthetic products and targets (half are mapped). One source name per 1,000
// matches "needle". Each case warms the request before the timed iteration.
//
// With TEST_DATABASE_URL set to a dedicated local *_test database, run:
//
//	go test -tags=integration ./tests/integration -run '^$' -bench '^BenchmarkCatalogProducts$' -benchmem -benchtime=1x -count=3
//
// Set STYK_CATALOG_EXPLAIN=1 and add -v to capture ANALYZE/BUFFERS plans for
// the actual page and matching queries with their synthetic parameters.
// On an i5-13600KF with Go 1.27.3 and local PostgreSQL, a single 10,000-product
// target-name search took 65.64 s before the SQL change; after it, the median of
// three single iterations was 35.1 ms. The revised response also includes Ozon
// and confirmed-target names. These are synthetic warm-request measurements,
// not provider latency or a claim about other workloads.
func BenchmarkCatalogProducts(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			f := newCatalogPerformanceFixture(b, size)
			for _, tc := range []struct{ name, search string }{
				{"first_page", ""}, {"target_name_search", "needle"},
			} {
				b.Run(tc.name, func(b *testing.B) {
					f.trace.reset()
					f.request(b, tc.search)
					if os.Getenv("STYK_CATALOG_EXPLAIN") == "1" && size >= 1000 {
						f.explain(b)
					}
					f.trace.stop()
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						f.request(b, tc.search)
					}
				})
			}
		})
	}
}

type tracedQuery struct {
	sql  string
	args []any
}

type catalogTracer struct {
	mu      sync.Mutex
	enabled bool
	queries []tracedQuery
}

func (t *catalogTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled && (strings.Contains(data.SQL, "WITH latest AS") || strings.Contains(data.SQL, "FROM source_entities WHERE import_id=") || strings.Contains(data.SQL, "FROM product_mappings m WHERE")) {
		t.queries = append(t.queries, tracedQuery{data.SQL, append([]any(nil), data.Args...)})
	}
	return ctx
}

func (t *catalogTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (t *catalogTracer) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queries, t.enabled = nil, true
}
func (t *catalogTracer) stop() []tracedQuery {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
	return append([]tracedQuery(nil), t.queries...)
}

type catalogPerformanceFixture struct {
	pool   *pgxpool.Pool
	trace  *catalogTracer
	h      http.Handler
	cookie *http.Cookie
}

func newCatalogPerformanceFixture(b testing.TB, size int) catalogPerformanceFixture {
	b.Helper()
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || !strings.HasSuffix(config.ConnConfig.Database, "_test") || !catalogLoopback(config.ConnConfig.Host) || len(config.ConnConfig.Fallbacks) != 0 {
		b.Fatal("catalog fixture requires a local test database whose name ends in _test")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		b.Fatal("dedicated test database unavailable")
	}
	b.Cleanup(admin.Close)
	if admin.Ping(ctx) != nil {
		b.Fatal("dedicated test database unavailable")
	}
	schema := createCatalogSchema(b, admin)
	config.ConnConfig.RuntimeParams["search_path"] = schema
	trace := &catalogTracer{}
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		b.Fatal("create isolated database pool")
	}
	b.Cleanup(pool.Close)
	catalogMigrate(b, pool)
	catalogSeed(b, pool, size)
	h, cookie := catalogHandler(b, pool)
	return catalogPerformanceFixture{pool: pool, trace: trace, h: h, cookie: cookie}
}

func createCatalogSchema(b testing.TB, admin *pgxpool.Pool) string {
	b.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		b.Fatal("cannot create isolated schema name")
	}
	schema := "catalog_bench_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		b.Fatalf("create schema: %v", err)
	}
	b.Cleanup(func() {
		cleanupIntegrationSchema(b, admin, schema)
	})
	return schema
}

func catalogLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func catalogMigrate(b testing.TB, pool *pgxpool.Pool) {
	b.Helper()
	paths, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(paths) == 0 {
		b.Fatal("migration files unavailable")
	}
	for _, path := range paths {
		file, err := os.ReadFile(path)
		if err != nil {
			b.Fatalf("read %s: %v", filepath.Base(path), err)
		}
		up, _, ok := strings.Cut(string(file), "-- +goose Down")
		if !ok {
			b.Fatalf("migration %s has no rollback section", filepath.Base(path))
		}
		if _, err = pool.Exec(context.Background(), up); err != nil {
			b.Fatalf("apply %s: %v", filepath.Base(path), err)
		}
	}
}

func catalogSeed(b testing.TB, pool *pgxpool.Pool, size int) {
	b.Helper()
	ctx := context.Background()
	var moyID, ozonID int64
	query := `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES($1,$2,'synthetic',now(),now(),'synthetic') RETURNING id`
	if err := pool.QueryRow(ctx, query, "moysklad", "moy").Scan(&moyID); err != nil {
		b.Fatalf("seed source import: %v", err)
	}
	if err := pool.QueryRow(ctx, query, "ozon", "ozon").Scan(&ozonID); err != nil {
		b.Fatalf("seed Ozon import: %v", err)
	}
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO saas_workspaces(id,name) VALUES('tenant','Synthetic')`, nil},
		{`INSERT INTO stock_connections(tenant_id,source,account_id,provenance,include_import_history) VALUES('tenant','moysklad','moy','synthetic',true),('tenant','ozon','ozon','synthetic',true)`, nil},
		{`INSERT INTO ozon_import_batches(import_id,traversal_status) VALUES($1,'completed')`, []any{ozonID}},
		{`INSERT INTO source_entities(import_id,kind,external_id,name,code,article,parent_id,source_updated) SELECT $1,'product','target-'||lpad(n::text,6,'0'),CASE WHEN n%1000=0 THEN 'needle special' ELSE 'synthetic target '||n END,'','offer-'||lpad(n::text,6,'0'),'','' FROM generate_series(1,$2::int) n`, []any{moyID, size}},
		{`INSERT INTO ozon_product_observations(import_id,product_id,offer_id,sku,archived) SELECT $1,'product-'||lpad(n::text,6,'0'),'offer-'||lpad(n::text,6,'0'),'sku-'||n,false FROM generate_series(1,$2::int) n`, []any{ozonID, size}},
		{`INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) SELECT 'tenant','ozon','product-'||lpad(n::text,6,'0'),'offer-'||lpad(n::text,6,'0'),'moy','product','target-'||lpad(n::text,6,'0'),1,'synthetic' FROM generate_series(1,$1::int) n WHERE n%2=0`, []any{size}},
		{`ANALYZE`, nil},
	}
	for _, q := range queries {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			b.Fatalf("seed synthetic catalog: %v", err)
		}
	}
}

func catalogHandler(b testing.TB, pool *pgxpool.Pool) (http.Handler, *http.Cookie) {
	b.Helper()
	secret := strings.Repeat("s", 32)
	h, err := console.Handler(&postgres.Store{Pool: pool, Timeout: 150 * time.Second}, secret, "http://127.0.0.1:5173")
	if err != nil {
		b.Fatalf("create console: %v", err)
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || len(w.Result().Cookies()) == 0 {
		b.Fatal("synthetic console authentication failed")
	}
	return h, w.Result().Cookies()[0]
}

func (f catalogPerformanceFixture) request(b testing.TB, search string) *httptest.ResponseRecorder {
	b.Helper()
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?tenant=tenant&ozon_account=ozon&moysklad_account=moy&search="+url.QueryEscape(search), nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("catalog request returned %d: %s", w.Code, w.Body.String())
	}
	return w
}

func (f catalogPerformanceFixture) explain(b *testing.B) {
	b.Helper()
	queries := f.trace.stop()
	for i, query := range queries {
		rows, err := f.pool.Query(context.Background(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) "+query.sql, query.args...)
		if err != nil {
			b.Fatalf("explain query %d: %v", i, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				b.Fatalf("read query plan: %v", err)
			}
			plan.WriteString(line)
			plan.WriteByte('\n')
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			b.Fatalf("read query plan: %v", err)
		}
		b.Logf("query %d:\n%s", i, plan.String())
	}
}

// Exercise the searched target set, confirmed target details, and nullable Ozon
// names through the authenticated API on a migrated, isolated schema.
func TestCatalogProductSearchAndDetails(t *testing.T) {
	f := newCatalogPerformanceFixture(t, 100)
	seedCatalogSearchDetails(t, f)
	tests := []struct {
		name, search, productID, status string
		candidates                      int
	}{
		{"unicode article", "gReEn", "product-000001", "ONE_CANDIDATE", 1},
		{"mapped name without candidate", "turquoise", "product-000002", "CONFIRMED", 0},
		{"candidate by code", "Alpha", "product-000003", "ONE_CANDIDATE", 1},
		{"marketplace name", "sAbLe", "product-000003", "ONE_CANDIDATE", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := readCatalogPage(t, f, tc.search)
			if len(page.Items) != 1 || page.Items[0].ProductID != tc.productID || page.Items[0].Status != tc.status || page.Items[0].CandidateCount != tc.candidates {
				t.Fatalf("unexpected search result: %+v", page)
			}
		})
	}
	seedOtherMappingAccount(t, f)
	page := readCatalogPage(t, f, "")
	if len(page.Items) != 50 || !page.HasNext || page.Items[0].Name != nil || page.Items[1].TargetName == nil || *page.Items[1].TargetName != "Mapped turquoise" || page.Items[1].TargetArticle == nil || *page.Items[1].TargetArticle != "different" || page.Items[2].Name == nil || *page.Items[2].Name != "Marketplace sable" {
		t.Fatalf("catalog names, mapping details, or pagination incorrect: %+v", page)
	}
	if page.Items[3].Name != nil || page.Items[5].TargetName != nil || page.Items[5].TargetArticle != nil {
		t.Fatal("empty names or articles were presented as populated")
	}
	assertEmptyCatalogFieldsOmitted(t, f)
	if page.Items[3].Status != "CONFLICT" || page.Items[3].TargetName != nil || page.Items[3].TargetArticle != nil {
		t.Fatal("mapping to another account was presented as an in-scope target")
	}
	if len(readCatalogPage(t, f, "foreign marker").Items) != 0 {
		t.Fatal("search exposed a target from another source account")
	}
	testCatalogScopeAndHistoricalTarget(t, f)
}

func seedCatalogSearchDetails(t *testing.T, f catalogPerformanceFixture) {
	t.Helper()
	ctx := context.Background()
	updates := []struct {
		sql  string
		args []any
	}{
		{`UPDATE source_entities SET name='Rare green',article=$1 WHERE external_id='target-000001'`, []any{"\u00a0offer-000001\u00a0"}},
		{`UPDATE source_entities SET name='Mapped turquoise',article='different' WHERE external_id='target-000002'`, nil},
		{`UPDATE source_entities SET name='Alpha code',article='different',code='offer-000003' WHERE external_id='target-000003'`, nil},
		{`UPDATE ozon_product_observations SET name='Marketplace sable' WHERE product_id='product-000003'`, nil},
		{`UPDATE ozon_product_observations SET name='' WHERE product_id='product-000004'`, nil},
		{`UPDATE source_entities SET name='',article='' WHERE external_id='target-000006'`, nil},
	}
	for _, update := range updates {
		if _, err := f.pool.Exec(ctx, update.sql, update.args...); err != nil {
			t.Fatalf("prepare catalog search: %v", err)
		}
	}
}

func assertEmptyCatalogFieldsOmitted(t *testing.T, f catalogPerformanceFixture) {
	t.Helper()
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(f.request(t, "").Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw catalog page: %v", err)
	}
	if _, present := raw.Items[3]["name"]; present {
		t.Fatal("empty marketplace name was not omitted")
	}
	for _, field := range []string{"target_name", "target_article"} {
		if _, present := raw.Items[5][field]; present {
			t.Fatalf("empty %s was not omitted", field)
		}
	}
}

func seedOtherMappingAccount(t *testing.T, f catalogPerformanceFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `INSERT INTO stock_connections(tenant_id,source,account_id,provenance,include_import_history) VALUES('tenant','moysklad','other-moy','synthetic',true)`); err != nil {
		t.Fatalf("register second source account: %v", err)
	}
	var importID int64
	err := f.pool.QueryRow(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('moysklad','other-moy','synthetic',now(),now(),'synthetic') RETURNING id`).Scan(&importID)
	if err != nil {
		t.Fatalf("seed second source import: %v", err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO source_entities(import_id,kind,external_id,name,code,article,parent_id,source_updated) VALUES($1,'product','target-000004','foreign marker','','foreign','','')`, importID); err != nil {
		t.Fatalf("seed second account target: %v", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE product_mappings SET moysklad_account='other-moy',version=3 WHERE ozon_product_id='product-000004'`); err != nil {
		t.Fatalf("seed other-account mapping revision: %v", err)
	}
}

type catalogPage struct {
	Items []struct {
		ProductID      string  `json:"product_id"`
		Name           *string `json:"name"`
		TargetName     *string `json:"target_name"`
		TargetArticle  *string `json:"target_article"`
		Status         string  `json:"match_status"`
		CandidateCount int     `json:"candidate_count"`
	} `json:"items"`
	HasNext bool `json:"has_next"`
}

func readCatalogPage(t *testing.T, f catalogPerformanceFixture, search string) catalogPage {
	t.Helper()
	var page catalogPage
	if err := json.Unmarshal(f.request(t, search).Body.Bytes(), &page); err != nil {
		t.Fatalf("decode catalog page: %v", err)
	}
	return page
}

func testCatalogScopeAndHistoricalTarget(t *testing.T, f catalogPerformanceFixture) {
	t.Helper()
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?tenant=other&ozon_account=ozon&moysklad_account=moy&search=turquoise", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "turquoise") {
		t.Fatal("cross-tenant catalog data exposed")
	}
	_, err := f.pool.Exec(context.Background(), `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('moysklad','moy','newer',now(),now(),'synthetic')`)
	if err != nil {
		t.Fatalf("seed latest source import: %v", err)
	}
	page := readCatalogPage(t, f, "")
	if page.Items[1].Status != "CONFIRMED" || page.Items[1].TargetName != nil || page.Items[1].TargetArticle != nil {
		t.Fatal("stale source details exposed or historical confirmation lost")
	}
	if len(readCatalogPage(t, f, "turquoise").Items) != 0 {
		t.Fatal("search exposed a target from an older source import")
	}
}
