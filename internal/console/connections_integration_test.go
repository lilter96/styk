//go:build integration

package console

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

func connectionFixture(t *testing.T, accountSuffix string) (*postgres.Store, http.Handler, *httptest.Server) {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_connection_v1_test" {
		t.Skip("dedicated local styk_connection_v1_test database required")
	}
	db, err := postgres.Open(context.Background(), u.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	msAccount := "synthetic-moy-" + accountSuffix
	ozAccount := "synthetic-ozon-" + accountSuffix
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/entity/product":
			fmt.Fprintf(w, `{"meta":{"size":1},"rows":[{"id":"synthetic","accountId":%q}]}`, msAccount)
		case r.Method == "POST" && r.URL.Path == "/v3/product/list":
			fmt.Fprint(w, `{"result":{"items":[]}}`)
		default:
			t.Error("unexpected external operation")
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(mock.Close)
	m, _ := moysklad.New(moysklad.Options{BaseURL: mock.URL + "/", Token: "synthetic-only-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	o, _ := ozon.New(ozon.Options{BaseURL: mock.URL, ClientID: ozAccount, APIKey: "synthetic-only-key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	c := &connections{db: db, moyAccount: msAccount, ozonAccount: ozAccount, moy: m, ozon: o, checks: map[string]map[string]connectionCheck{}}
	handler, err := handlerWithConnections(db, strings.Repeat("s", 32), "http://127.0.0.1:15174", c)
	if err != nil {
		t.Fatal(err)
	}
	return db, handler, mock
}

func TestConnectionBrowser(t *testing.T) {
	if os.Getenv("STYK_BROWSER_E2E") != "1" {
		t.Skip("browser E2E not requested")
	}
	suffix := fmt.Sprintf("browser-%d", time.Now().UnixNano())
	db, h, _ := connectionFixture(t, suffix)
	api := httptest.NewServer(h)
	defer api.Close()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "connection-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(config.Name())
	_, err = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15174,strictPort:true,proxy:{'/api':%q}}});`, api.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", config.Name())
	vite.Dir = web
	var log bytes.Buffer
	vite.Stdout = &log
	vite.Stderr = &log
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vite.Process.Kill(); _ = vite.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		resp, e := http.Get("http://127.0.0.1:15174/")
		if e == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("Vite did not start: %s", log.String())
	}
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/integrations.spec.ts", "--workers=1")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_E2E_API_URL="+api.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser E2E: %v\n%s", err, output)
	}
	t.Log(string(output))
	var count int
	var provenance string
	var history bool
	if err = db.Pool.QueryRow(ctx, `SELECT count(*),max(provenance),bool_or(include_import_history) FROM stock_connections WHERE tenant_id='browser-tenant' AND source='moysklad' AND account_id=$1`, "synthetic-moy-"+suffix).Scan(&count, &provenance, &history); err != nil || count != 1 || !strings.HasPrefix(provenance, "LOCAL_OPERATOR_SESSION:") || history {
		t.Fatalf("browser registration not persisted correctly: %v", err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*),max(provenance),bool_or(include_import_history) FROM stock_connections WHERE tenant_id='browser-tenant' AND source='ozon' AND account_id=$1`, "synthetic-ozon-"+suffix).Scan(&count, &provenance, &history); err != nil || count != 1 || !strings.HasPrefix(provenance, "LOCAL_OPERATOR_SESSION:") || history {
		t.Fatalf("browser Ozon registration not persisted correctly: %v", err)
	}
	for _, table := range []string{"source_imports", "outbox_tasks", "stock_decisions", "stock_pools", "product_mappings", "warehouse_mappings"} {
		if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected browser side effect %s", table)
		}
	}
}
func TestConnectionRegistrationIntegration(t *testing.T) {
	suffix := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	db, h, _ := connectionFixture(t, suffix)
	request := func(method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:18081"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const origin = "http://127.0.0.1:15174"
	if w := request("POST", "/api/integrations/moysklad/register", `{"tenant":"synthetic-tenant","acknowledge":true}`, nil, origin); w.Code != 401 {
		t.Fatal("unauthenticated registration")
	}
	login := request("POST", "/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil, origin)
	if login.Code != 204 {
		t.Fatal("login failed")
	}
	cookie := login.Result().Cookies()[0]
	if w := request("POST", "/api/integrations/moysklad/probe", `{}`, cookie, "http://evil.invalid"); w.Code != 403 {
		t.Fatal("invalid origin accepted")
	}
	if w := request("POST", "/api/integrations/moysklad/register", `{"tenant":"synthetic-tenant","acknowledge":true}`, cookie, origin); w.Code != 409 || !strings.Contains(w.Body.String(), "PROBE_REQUIRED") {
		t.Fatal("registration allowed without probe")
	}
	if w := request("POST", "/api/integrations/moysklad/probe", `{}`, cookie, origin); w.Code != 200 || !strings.Contains(w.Body.String(), "VERIFIED") {
		t.Fatal("probe failed")
	}
	if w := request("POST", "/api/integrations/moysklad/register", `{"tenant":"synthetic-tenant","acknowledge":true,"account_id":"fake"}`, cookie, origin); w.Code != 400 {
		t.Fatal("browser-supplied identity accepted")
	}
	body := `{"tenant":"synthetic-tenant","acknowledge":true}`
	if w := request("POST", "/api/integrations/moysklad/register", body, cookie, origin); w.Code != 200 {
		t.Fatalf("registration failed: %s", w.Body.String())
	}
	if w := request("POST", "/api/integrations/moysklad/register", body, cookie, origin); w.Code != 200 || !strings.Contains(w.Body.String(), "ALREADY_REGISTERED") {
		t.Fatal("duplicate not reconciled")
	}
	if w := request("POST", "/api/integrations/moysklad/register", `{"tenant":"another-tenant","acknowledge":true}`, cookie, origin); w.Code != 409 || !strings.Contains(w.Body.String(), "OWNERSHIP_CONFLICT") {
		t.Fatal("ownership conflict allowed")
	}
	if w := request("GET", "/api/integrations?tenant=synthetic-tenant", "", cookie, ""); w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-only") || !strings.Contains(w.Body.String(), "synthetic-ozon-"+suffix) {
		t.Fatal("bad status or credential leak")
	}
	var count int
	var provenance string
	var history bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*),max(provenance),bool_or(include_import_history) FROM stock_connections WHERE source='moysklad' AND account_id=$1 AND tenant_id='synthetic-tenant'`, "synthetic-moy-"+suffix).Scan(&count, &provenance, &history); err != nil || count != 1 || !strings.HasPrefix(provenance, "LOCAL_OPERATOR_SESSION:") || history {
		t.Fatalf("invalid registration: %v", err)
	}
	for _, table := range []string{"source_imports", "outbox_tasks", "stock_decisions", "stock_pools", "product_mappings", "warehouse_mappings"} {
		if err := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected side effect %s", table)
		}
	}
}

func TestRegistrationPersistenceFailureDoesNotLeavePartialState(t *testing.T) {
	suffix := fmt.Sprintf("rollback-%d", time.Now().UnixNano())
	db, h, _ := connectionFixture(t, suffix)
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `CREATE FUNCTION test_connection_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.account_id LIKE 'synthetic-moy-rollback-%' THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_connection_failure BEFORE INSERT ON stock_connections FOR EACH ROW EXECUTE FUNCTION test_connection_failure()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_connection_failure ON stock_connections; DROP FUNCTION IF EXISTS test_connection_failure()`)
	})
	call := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:18081"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://127.0.0.1:15174")
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := call("/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil)
	cookie := login.Result().Cookies()[0]
	if w := call("/api/integrations/moysklad/probe", `{}`, cookie); w.Code != 200 {
		t.Fatal("probe failed")
	}
	if w := call("/api/integrations/moysklad/register", `{"tenant":"rollback-tenant","acknowledge":true}`, cookie); w.Code != 503 || !strings.Contains(w.Body.String(), "REGISTRATION_OUTCOME_UNKNOWN") {
		t.Fatal("failure not reported conservatively")
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_connections WHERE source='moysklad' AND account_id=$1`, "synthetic-moy-"+suffix).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed registration left a record")
	}
}

func TestConcurrentRegistrationDoesNotTransferAccount(t *testing.T) {
	suffix := fmt.Sprintf("concurrent-%d", time.Now().UnixNano())
	db, h, _ := connectionFixture(t, suffix)
	call := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:18081"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://127.0.0.1:15174")
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := call("/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil)
	cookie := login.Result().Cookies()[0]
	if w := call("/api/integrations/moysklad/probe", `{}`, cookie); w.Code != 200 {
		t.Fatal("probe failed")
	}
	results := make(chan int, 2)
	for _, tenant := range []string{"race-tenant-a", "race-tenant-b"} {
		go func(tenant string) {
			w := call("/api/integrations/moysklad/register", fmt.Sprintf(`{"tenant":%q,"acknowledge":true}`, tenant), cookie)
			results <- w.Code
		}(tenant)
	}
	a, b := <-results, <-results
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("conflicting results: %d %d", a, b)
	}
	var count int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM stock_connections WHERE source='moysklad' AND account_id=$1`, "synthetic-moy-"+suffix).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent ownership was not unique")
	}
}
