//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

const browserAutoWorkspace = "synthetic-auto-browser-b"

func TestMerchantBrowserWorkspaces(t *testing.T) {
	if os.Getenv("STYK_MERCHANT_BROWSER_E2E") != "1" {
		t.Skip("merchant browser E2E not requested")
	}
	for _, mode := range []struct {
		name      string
		automatic bool
	}{{"unapproved", false}, {"loopback_simulator", true}} {
		t.Run(mode.name, func(t *testing.T) { runMerchantBrowser(t, mode.automatic) })
	}
}

func runMerchantBrowser(t *testing.T, automatic bool) {
	t.Helper()
	db, _ := controlledFixture(t)
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	t.Setenv("STYK_MERCHANT_AUTOSIM", "")
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_API_KEY", "OZON_CLIENT_ID"} {
		t.Setenv(key, "")
	}
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		t.Fatal("synthetic credential vault unavailable")
	}
	seedMerchantBrowserCredentials(t, db, vault)
	provider := &merchantFakeProvider{}
	providerServer := httptest.NewServer(provider)
	defer providerServer.Close()
	if automatic {
		t.Setenv("STYK_MERCHANT_AUTOSIM", "1")
		t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
		t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", providerServer.URL)
		// Only this disposable test schema admits the existing loopback simulator.
		if _, err = db.Pool.Exec(context.Background(), `ALTER TABLE stock_auto_configs DROP CONSTRAINT stock_auto_configs_check`); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(context.Background(), `INSERT INTO saas_workspaces(id,name,legacy_unclaimed) VALUES($1,'Магазин B (симулятор)',true)`, browserAutoWorkspace); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "")
		t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	startMerchantImportWorker(t, ctx, db, vault, providerServer.URL, automatic)
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantLoopbackTestHandler(db, "http://127.0.0.1:15179", mailbox, providerServer.URL, providerServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := merchantBrowserAPI(t, db, mailbox, handler)
	defer api.Close()
	runMerchantPlaywright(t, ctx, api.URL)
	if !automatic && provider.writes.Load() != 0 {
		t.Fatal("unexpected publication for an unapproved merchant")
	}
	if automatic && (provider.writes.Load() != 1 || provider.stockB.Load() != 2) {
		t.Fatalf("simulator did not receive exactly one scoped publication: writes=%d stock=%d", provider.writes.Load(), provider.stockB.Load())
	}
	assertMerchantHistoryRollbackGuard(t, db, automatic)
}

func assertMerchantHistoryRollbackGuard(t *testing.T, db *postgres.Store, automatic bool) {
	t.Helper()
	var count, enabled int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER (WHERE enabled) FROM stock_auto_configs WHERE tenant_id<>$1`, testpublication.Tenant).Scan(&count, &enabled); err != nil || count != 1 || (enabled == 1) != automatic {
		t.Fatalf("unexpected merchant policy count=%d enabled=%d error=%v", count, enabled, err)
	}
	cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "refusing to discard merchant ownership bindings") && !strings.Contains(string(output), "refusing to discard observed Ozon product names") {
		t.Fatal("migration rollback did not protect merchant publication scope")
	}
}

func seedMerchantBrowserCredentials(t *testing.T, db *postgres.Store, vault *credentials.Vault) {
	t.Helper()
	ctx := context.Background()
	scope := testpublication.Scope()
	for _, entry := range []struct{ provider, account, secret string }{
		{"moysklad", scope.MoyskladAccount, "synthetic-moy-a"},
		{"ozon", scope.OzonAccount, "synthetic-ozon-a"},
	} {
		key := credentials.Scope{Tenant: scope.Tenant, Provider: entry.provider, Account: entry.account}
		ciphertext, err := vault.Encrypt(key, 1, entry.secret)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = db.SaveCredential(ctx, key, "Тест A", 0, "synthetic-browser-"+entry.provider, vault.Digest([]byte(entry.provider)), ciphertext, time.Now(), "synthetic fixture"); err != nil {
			t.Fatal(err)
		}
	}
}

func startMerchantImportWorker(t *testing.T, ctx context.Context, db *postgres.Store, vault *credentials.Vault, base string, automatic bool) {
	t.Helper()
	resolver := &credentials.Resolver{Store: db, Vault: vault}
	imports := ozonjob.Runner{Store: db, Resolver: resolver, NewOzon: func(account, secret string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{BaseURL: base, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, IncludeProductNames: true})
	}, NewMoysklad: func(_ string, secret string) (*moysklad.Client, error) {
		return moysklad.New(moysklad.Options{BaseURL: base, Token: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	}}
	worker := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: imports, StoredCredentials: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 50 * time.Millisecond, Lease: time.Minute, Retry: time.Second}
	if automatic {
		publisher := stocksync.Runner{Enabled: true, Store: db, ResolveOzon: func(ctx context.Context, tenant, account string) (*ozon.Client, error) {
			if tenant != browserAutoWorkspace || account != browserOzon {
				return nil, errors.New("synthetic publication scope mismatch")
			}
			secret, err := resolver.Resolve(ctx, credentials.Scope{Tenant: tenant, Provider: "ozon", Account: account})
			if err != nil {
				return nil, err
			}
			return ozon.New(ozon.Options{BaseURL: base, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
		}}
		worker.Publications = publisher
		worker.Automatic = stocksync.AutomaticRunner{Store: db, Publisher: publisher, Log: worker.Log}
	}
	done := make(chan error, 1)
	workerCtx, stop := context.WithCancel(ctx)
	go func() { done <- worker.Run(workerCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("stop synthetic worker: %v", err)
		}
	})
}

func merchantBrowserAPI(t *testing.T, db *postgres.Store, mailbox *testMailbox, handler http.Handler) *httptest.Server {
	t.Helper()
	service, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__test/mail", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Secret") != "synthetic-browser-harness" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		purpose := r.URL.Query().Get("purpose")
		if purpose == "" {
			purpose = "VERIFY_EMAIL"
		}
		if purpose != "VERIFY_EMAIL" && purpose != "RESET_PASSWORD" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": mailbox.token(r.URL.Query().Get("email"), purpose)})
	})
	mux.HandleFunc("POST /__test/claim", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Secret") != "synthetic-browser-harness" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body struct {
			Email     string `json:"email"`
			Workspace string `json:"workspace"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Workspace == "" {
			body.Workspace = testpublication.Tenant
		}
		if body.Workspace != testpublication.Tenant && (body.Workspace != browserAutoWorkspace || os.Getenv("STYK_MERCHANT_AUTOSIM") != "1") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var user string
		if err := db.Pool.QueryRow(r.Context(), `SELECT id FROM saas_users WHERE email=$1`, body.Email).Scan(&user); err != nil || service.ClaimLegacy(r.Context(), body.Workspace, user) != nil || service.BindLegacyMerchant(r.Context(), user, body.Workspace) != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", handler)
	return httptest.NewServer(mux)
}

func runMerchantPlaywright(t *testing.T, ctx context.Context, apiURL string) {
	t.Helper()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	configFile, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "merchant-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Remove(configFile.Name()); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove temporary Vite config: %v", err)
		}
	}()
	if _, err = fmt.Fprintf(configFile, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15179,strictPort:true,proxy:{'/api':{target:%q,changeOrigin:false}}}});`, apiURL); err != nil {
		t.Fatal(err)
	}
	if err = configFile.Close(); err != nil {
		t.Fatal(err)
	}
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", configFile.Name())
	vite.Dir = web
	var logs bytes.Buffer
	vite.Stdout, vite.Stderr = &logs, &logs
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vite.Process.Kill(); _ = vite.Wait() }()
	waitMerchantVite(t, &logs)
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/merchant-workspaces.spec.ts", "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_MERCHANT_ORIGIN=http://127.0.0.1:15179", "STYK_MERCHANT_TEST_API="+apiURL, "STYK_MERCHANT_TEST_SECRET=synthetic-browser-harness")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("merchant browser failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func waitMerchantVite(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	for i := 0; i < 100; i++ {
		resp, err := http.Get("http://127.0.0.1:15179/")
		if err == nil {
			if err = resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("synthetic browser server unavailable: %s", logs.String())
}
