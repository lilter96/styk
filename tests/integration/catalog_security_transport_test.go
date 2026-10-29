//go:build integration

package integration

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
)

type catalogSecurityImport struct {
	db                        *postgres.Store
	tenant, provider, account string
}

func newCatalogSecurityStore(t *testing.T) *postgres.Store {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || !strings.HasSuffix(config.ConnConfig.Database, "_test") || !catalogLoopback(config.ConnConfig.Host) || len(config.ConnConfig.Fallbacks) != 0 {
		t.Fatal("catalog security tests require a dedicated local *_test database")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("open isolated test database")
	}
	t.Cleanup(admin.Close)
	schema := createCatalogSchema(t, admin)
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("open isolated test schema")
	}
	t.Cleanup(pool.Close)
	catalogMigrate(t, pool)
	return &postgres.Store{Pool: pool, Timeout: 5 * time.Second}
}

func newCatalogSecurityImport(t *testing.T, provider string) catalogSecurityImport {
	t.Helper()
	db := newCatalogSecurityStore(t)
	schema := db.Pool.Config().ConnConfig.RuntimeParams["search_path"]
	f := catalogSecurityImport{db: db, tenant: schema, provider: provider, account: schema + "-" + provider}
	if err := db.RegisterStockConnection(t.Context(), f.tenant, provider, f.account, "synthetic security test", false); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f catalogSecurityImport) submit(t *testing.T, key string) (domain.ImportOperation, domain.Task) {
	t.Helper()
	limits := domain.DefaultOzonImportLimits()
	if f.provider == "moysklad" {
		limits = domain.DefaultMoyskladImportLimits()
	}
	limits.ResponseBytes = 1024
	op, _, err := f.db.SubmitCatalogImport(t.Context(), f.tenant, f.provider, f.account, key, "synthetic security session", limits)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.db.ClaimForCatalogWorker(t.Context(), time.Minute, f.account, f.account)
	if err != nil || claim == nil || claim.ImportOperationID != op.ID {
		t.Fatalf("claim scoped security import: %v", err)
	}
	return op, *claim
}

func (f catalogSecurityImport) runner(t *testing.T, base string) ozonjob.Runner {
	t.Helper()
	runner := ozonjob.Runner{Store: f.db}
	if f.provider == "moysklad" {
		client, err := moysklad.New(moysklad.Options{BaseURL: base, Token: "synthetic-security-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		runner.MoyskladClient, runner.MoyskladAccount = client, f.account
		return runner
	}
	client, err := ozon.New(ozon.Options{BaseURL: base, ClientID: f.account, APIKey: "synthetic-security-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	runner.Client, runner.Account = client, f.account
	return runner
}

func TestCatalogSecurityImportRejectsRedirectsAndCompressedOversize(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, outcome, resource string }{
		{name: "redirect", outcome: "API_FAILURE"},
		{name: "compressed_oversize", outcome: "LIMIT_REACHED", resource: "response_bytes"},
	}
	for _, provider := range []string{"moysklad", "ozon"} {
		for _, tc := range tests {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				f := newCatalogSecurityImport(t, provider)
				op, claim := f.submit(t, tc.name)
				var redirects, attempts atomic.Int32
				sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					redirects.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				defer sink.Close()
				server := catalogSecurityFailureServer(t, tc.name, sink.URL, &attempts)
				defer server.Close()
				if err := f.runner(t, server.URL).ProcessImport(t.Context(), claim, time.Minute); err != nil {
					t.Fatalf("persist rejected import outcome: %v", err)
				}
				f.assertRejected(t, op.ID, tc.outcome, tc.resource)
				if attempts.Load() != 1 || redirects.Load() != 0 {
					t.Fatalf("unsafe transport: attempts=%d redirected=%d", attempts.Load(), redirects.Load())
				}
			})
		}
	}
}

func catalogSecurityFailureServer(t *testing.T, mode, sink string, attempts *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if mode == "redirect" {
			http.Redirect(w, r, sink+"/credential-sink", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		if _, err := writer.Write([]byte(`{"untrusted":"` + strings.Repeat("synthetic-provider-body", 1024) + `"}`)); err != nil {
			t.Errorf("write synthetic compressed response: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Errorf("close synthetic compressed response: %v", err)
		}
	}))
}

func (f catalogSecurityImport) assertRejected(t *testing.T, id, outcome, resource string) {
	t.Helper()
	op, err := f.db.GetCatalogOperation(t.Context(), f.tenant, f.provider, f.account, id)
	if err != nil {
		t.Fatal(err)
	}
	if op.State != "FAILED" || op.Outcome == nil || *op.Outcome != outcome || op.BatchID != nil || op.Counts.Attempts != 1 {
		t.Fatalf("rejection was not recorded without a batch: %+v", op)
	}
	if resource != "" && (op.LimitingResource == nil || *op.LimitingResource != resource) {
		t.Fatalf("wrong limiting resource: %v", op.LimitingResource)
	}
	var count int
	if err = f.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM source_imports WHERE source=$1 AND account_id=$2`, f.provider, f.account).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected provider response persisted: batches=%d error=%v", count, err)
	}
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-provider-body") || strings.Contains(string(raw), "synthetic-security-token") || strings.Contains(string(raw), "credential-sink") {
		t.Fatal("provider-controlled content leaked into durable operation results")
	}
}
