//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

func ordersProcessSafety(t testing.TB, dsn string) {
	t.Helper()
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_API_KEY", "OZON_CLIENT_ID"} {
		if os.Getenv(key) != "" {
			t.Fatal("orders process tests require blank live provider environment")
		}
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Path != "/styk_orders_store_test" {
		t.Fatal("orders process tests require the dedicated localhost orders test database")
	}
}

func ordersProcessLoopback(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.Path == "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func ordersProcessDB(t *testing.T) (*postgres.Store, *credentials.Vault) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		raw, err := os.ReadFile("../../.local/v7/test-db-dsn")
		if err != nil {
			t.Fatal("orders process test database DSN unavailable")
		}
		dsn = strings.TrimSpace(string(raw))
	}
	ordersProcessSafety(t, dsn)
	t.Setenv("TEST_DATABASE_URL", dsn)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{47}, 32)))
	for _, key := range []string{"STYK_TEST_ACCOUNT_ORDERS", "STYK_TEST_ACCOUNT_PUBLICATION", "STYK_STOCK_PUBLICATION_ENABLED", "STYK_STOCK_PUBLICATION_TEST_ENDPOINT"} {
		t.Setenv(key, "")
	}
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		t.Fatal("create synthetic orders process vault")
	}
	return catalogSyncDB(t), vault
}

type ordersProcessLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *ordersProcessLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *ordersProcessLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type ordersProcessChild struct {
	settings ordersProcessSettings
	cmd      *exec.Cmd
	done     chan error
	url      string
	logs     *ordersProcessLog
	stopped  bool
}

func ordersProcessStart(t *testing.T, settings ordersProcessSettings) *ordersProcessChild {
	t.Helper()
	dir := t.TempDir()
	settings.Ready = filepath.Join(dir, "ready")
	path := filepath.Join(dir, "settings.json")
	raw, err := json.Marshal(settings)
	if err != nil || os.WriteFile(path, raw, 0o600) != nil {
		t.Fatal("write private orders process settings")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := &ordersProcessChild{settings: settings, done: make(chan error, 1), logs: &ordersProcessLog{}}
	child.cmd = exec.Command(executable, "-test.run=^TestOrdersProcessChild$", "-test.count=1")
	child.cmd.Env = append(os.Environ(), "STYK_ORDERS_PROCESS_SETTINGS="+path, "SSL_CERT_FILE="+settings.MailCA)
	child.cmd.Stdout, child.cmd.Stderr = child.logs, child.logs
	if err = child.cmd.Start(); err != nil {
		t.Fatal("start independent orders process")
	}
	go func() { child.done <- child.cmd.Wait() }()
	t.Cleanup(func() { child.stop(t, false) })
	ordersProcessEventually(t, 10*time.Second, "orders replica readiness", func() bool {
		ready, readErr := os.ReadFile(settings.Ready)
		child.url = string(ready)
		return readErr == nil && ordersProcessLoopback(child.url)
	})
	return child
}

func (p *ordersProcessChild) stop(t *testing.T, kill bool) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	var err error
	if kill {
		err = p.cmd.Process.Kill()
	} else {
		err = p.cmd.Process.Signal(os.Interrupt)
	}
	if err != nil {
		t.Error("stop independent orders process")
	}
	select {
	case err = <-p.done:
		if err != nil && !kill {
			t.Errorf("orders %s process failed: %s", p.settings.Role, p.logs.String())
		}
	case <-time.After(8 * time.Second):
		if err = p.cmd.Process.Kill(); err != nil {
			t.Error("kill unresponsive orders process")
		}
		<-p.done
		t.Error("orders process did not stop")
	}
}

func (p *ordersProcessChild) request(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, p.url+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Secret", p.settings.Secret)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("request loopback orders process control")
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error("close orders process control response")
		}
	}()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func (p *ordersProcessChild) configure(t *testing.T, enabled bool, barrier string, release bool) {
	t.Helper()
	status, _ := p.request(t, http.MethodPost, "/__test/control", map[string]any{"enabled": enabled, "barrier": barrier, "release": release})
	if status != http.StatusNoContent {
		t.Fatalf("configure orders process: %d", status)
	}
}

func ordersProcessEventually(t *testing.T, duration time.Duration, condition string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", condition)
}

func ordersProcessRandom(t *testing.T) string {
	t.Helper()
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(token[:])
}

func ordersProcessID(number int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", number) }

func ordersProcessSeed(t *testing.T, db *postgres.Store, vault *credentials.Vault, tenant string, index int) orders.Scope {
	t.Helper()
	scope := orders.Scope{Tenant: tenant, OzonAccount: fmt.Sprintf("90000%d", index), MoyskladAccount: ordersProcessID(index)}
	for _, provider := range []string{"moysklad", "ozon"} {
		account := scope.OzonAccount
		if provider == "moysklad" {
			account = scope.MoyskladAccount
		}
		if err := db.RegisterStockConnection(context.Background(), tenant, provider, account, "Synthetic process fixture", true); err != nil {
			t.Fatal(err)
		}
		ordersProcessCredential(t, db, vault, credentials.Scope{Tenant: tenant, Provider: provider, Account: account}, 0)
	}
	ordersProcessMappings(t, db, scope)
	return scope
}

func ordersProcessCredential(t *testing.T, db *postgres.Store, vault *credentials.Vault, scope credentials.Scope, previous int64) {
	t.Helper()
	secret := "synthetic-orders-" + scope.Provider + "-" + scope.Account
	ciphertext, err := vault.Encrypt(scope, previous+1, secret)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("process-credential-%s-%d", scope.Provider, previous+1)
	_, _, err = db.SaveCredential(context.Background(), scope, "Synthetic process", previous, key, vault.Digest([]byte(key)), ciphertext, time.Now(), "synthetic-process-fixture")
	if err != nil {
		t.Fatal(err)
	}
}

func ordersProcessMappings(t *testing.T, db *postgres.Store, scope orders.Scope) {
	t.Helper()
	ctx, now, rfbs := context.Background(), time.Now().UTC(), false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: ordersProcessID(8), Name: "Synthetic process item", Article: "process-offer"}, {Kind: "store", ID: ordersProcessID(4), Name: "Synthetic process store"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "process-offer", SKU: "2001"}}, Warehouses: []domain.OzonWarehouse{{ID: "3001", Name: "Synthetic process warehouse", IsRFBS: &rfbs}}}
	if _, err := db.SaveInventoryImport(ctx, source, "process-source-"+scope.Tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "process-market-"+scope.Tenant); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	if _, err := db.ConfirmMatch(ctx, match, "1001", "product", ordersProcessID(8), "process-fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConfirmWarehouseMapping(ctx, match, ordersProcessID(4), "3001", "process-fixture"); err != nil {
		t.Fatal(err)
	}
}

func ordersProcessConfig(scope orders.Scope) orders.Config {
	return orders.Config{Scope: scope, Enabled: true, TestEnabled: true, IntervalSeconds: 30, SourceFreshnessSeconds: 3600, PreviewTTLSeconds: 300,
		OrganizationID: ordersProcessID(2), AgentID: ordersProcessID(3), StoreID: ordersProcessID(4), StateID: ordersProcessID(5), SalesChannelID: ordersProcessID(6), CurrencyID: ordersProcessID(7),
		FieldMappings: []orders.FieldMapping{{Source: "posting_number", Target: "name"}, {Source: "lines.quantity", Target: "positions.quantity"}, {Source: "lines.price", Target: "positions.price"}, {Source: "lines.vat", Target: "positions.vat"}}}
}
