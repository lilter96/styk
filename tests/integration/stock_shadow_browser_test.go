//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
)

type shadowOutboundGuard struct {
	base    http.RoundTripper
	blocked atomic.Int32
}

func (g *shadowOutboundGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	ip := net.ParseIP(req.URL.Hostname())
	if ip == nil || !ip.IsLoopback() || (req.Method == http.MethodPost && req.URL.Path == "/v2/products/stocks") {
		g.blocked.Add(1)
		return nil, errors.New("external provider request blocked in synthetic test")
	}
	return g.base.RoundTrip(req)
}

// The browser talks to the real console and a dedicated synthetic database.
// No publication runner or marketplace HTTP client is started.
func TestStockShadowBrowser(t *testing.T) {
	if os.Getenv("STYK_SHADOW_BROWSER_E2E") != "1" {
		t.Skip("shadow browser E2E not requested")
	}
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("OZON_CLIENT_ID", "")
	t.Setenv("OZON_API_KEY", "")
	t.Setenv("MOYSKLAD_TOKEN", "")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", "")
	previousTransport := http.DefaultTransport
	guard := &shadowOutboundGuard{base: previousTransport}
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	f := publicationData(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	newer := f.source
	newer.StartedAt = time.Now().UTC().Add(-10 * time.Second)
	newer.FinishedAt = newer.StartedAt.Add(time.Second)
	newer.Entities = append([]domain.ExternalEntity(nil), f.source.Entities...)
	newer.Entities[0].Name = "Синтетическая футболка"
	newer.Entities[0].ParentID = "synthetic-parent"
	newer.Entities[1].Name = "Основной склад"
	newer.Entities[2].Name = "Синтетическая куртка"
	newer.Entities = append(newer.Entities, domain.ExternalEntity{Kind: "product", ID: "synthetic-parent", Name: "Синтетическая модель"})
	newer.WarehouseStock = append([]domain.WarehouseStockObservation(nil), f.source.WarehouseStock[:1]...)
	if _, err := f.db.SaveInventoryImport(ctx, newer, "shadow-browser-new-source-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	// Seed explicit synthetic operator evidence as a disabled review. This is
	// not an enablement intent and creates no publication or outbox task.
	review := f.preview(t, true)
	if len(review.Targets) != 2 || review.Targets[0].Quantity == nil || *review.Targets[0].Quantity != "8" || review.Targets[1].Quantity != nil {
		t.Fatalf("synthetic calculation fixture: %+v", review.Targets)
	}
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO stock_sync_settings(tenant_id,moysklad_account,ozon_account,enabled,review_id,updated_at) VALUES($1,$2,$3,false,$4,now())`, f.scope.Tenant, f.scope.MoyskladAccount, f.scope.OzonAccount, review.ID); err != nil {
		t.Fatal(err)
	}
	var initial [6]int
	snapshot := func(result *[6]int) error {
		return f.db.Pool.QueryRow(ctx, `SELECT
   (SELECT count(*) FROM stock_publications WHERE tenant_id=$1),
   (SELECT count(*) FROM stock_sync_mutations WHERE tenant_id=$1),
   (SELECT count(*) FROM product_mapping_confirmation_audits WHERE tenant_id=$1) + (SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id=$1),
   (SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),
   (SELECT count(*) FROM stock_sync_reviews WHERE tenant_id=$1),
   (SELECT count(*) FROM outbox_tasks WHERE import_tenant_id=$1)`, f.scope.Tenant).Scan(&result[0], &result[1], &result[2], &result[3], &result[4], &result[5])
	}
	if err := snapshot(&initial); err != nil {
		t.Fatal(err)
	}
	if initial != [6]int{0, 0, 0, 0, 1, 0} {
		t.Fatalf("unexpected pre-browser state: %v", initial)
	}
	const origin = "http://127.0.0.1:15176"
	const secret = "synthetic-shadow-browser-operator-secret"
	handler, err := console.Handler(f.db, secret, origin)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(handler)
	defer api.Close()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "shadow-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer removeFixtureFile(t, config.Name())
	if _, err = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15176,strictPort:true,proxy:{'/api':%q}}});`, api.URL); err != nil {
		t.Fatal(err)
	}
	if err = config.Close(); err != nil {
		t.Fatal(err)
	}
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", config.Name())
	vite.Dir = web
	var logs bytes.Buffer
	vite.Stdout, vite.Stderr = &logs, &logs
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vite.Process.Kill(); _ = vite.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		resp, e := http.Get(origin)
		if e == nil {
			closeFixture(t, resp.Body)
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("synthetic Vite failed to start: %s", logs.String())
	}
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/stock-shadow.spec.ts", "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_SHADOW_E2E_ORIGIN="+origin, "STYK_SHADOW_E2E_SECRET="+secret, "STYK_SHADOW_E2E_TENANT="+f.scope.Tenant, "STYK_SHADOW_E2E_MOY="+f.scope.MoyskladAccount, "STYK_SHADOW_E2E_OZON="+f.scope.OzonAccount)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shadow browser: %v\n%s", err, output)
	}
	t.Log(string(output))
	var after [6]int
	if err = snapshot(&after); err != nil || initial != after {
		t.Fatalf("shadow mutated database: before=%v after=%v err=%v", initial, after, err)
	}
	if strings.Contains(string(output), "external-stock-write") {
		t.Fatal("external stock write detected")
	}
	if guard.blocked.Load() != 0 {
		t.Fatalf("unexpected external provider requests: %d", guard.blocked.Load())
	}
}
