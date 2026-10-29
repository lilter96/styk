//go:build integration

package console

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
)

// TestImportManagementBrowser uses the actual API, outbox worker and importer,
// but only a synthetic, explicitly allowlisted HTTP server and disposable DB.
func TestImportManagementBrowser(t *testing.T) {
	if os.Getenv("STYK_IMPORT_BROWSER_E2E") != "1" {
		t.Skip("import browser E2E not requested")
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_import_ui_e2e_v1" {
		t.Fatal("dedicated local styk_import_ui_e2e_v1 database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, u.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant, account := "browser-import-"+suffix, "browser-ozon-"+suffix
	t.Setenv("OZON_CLIENT_ID", account)
	t.Setenv("OZON_API_KEY", "synthetic-import-key")
	t.Setenv("MOYSKLAD_TOKEN", "")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", "")
	if err := db.RegisterStockConnection(ctx, tenant, "ozon", account, "synthetic browser fixture", false); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	started := make(chan struct{})
	var gateOnce, startedOnce sync.Once
	var callsMu sync.Mutex
	var calls []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsMu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		callsMu.Unlock()
		if r.Method != http.MethodPost || r.Header.Get("Client-Id") != account || r.Header.Get("Api-Key") != "synthetic-import-key" {
			t.Error("unexpected method or synthetic credentials")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/product/list":
			var body struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid synthetic request")
				w.WriteHeader(400)
				return
			}
			if body.Filter.Visibility == "ARCHIVED" {
				fmt.Fprint(w, `{"result":{"items":[],"total_items":0}}`)
				return
			}
			startedOnce.Do(func() { close(started) })
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"result":{"items":[{"product_id":1,"offer_id":"synthetic-offer","archived":false}],"total_items":1}}`)
		case "/v2/warehouse/list":
			fmt.Fprint(w, `{"warehouses":[{"warehouse_id":10,"name":"Synthetic FBS","status":"created","is_rfbs":false}],"has_next":false}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			fmt.Fprint(w, `{"products":[{"product_id":1,"offer_id":"synthetic-offer","warehouse_id":10,"warehouse_name":"Synthetic FBS","present":0,"reserved":0,"free_stock":0}],"has_next":false}`)
		default:
			t.Error("unallowlisted marketplace operation")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer mock.Close()
	client, err := ozon.New(ozon.Options{BaseURL: mock.URL, ClientID: account, APIKey: "synthetic-import-key", Timeout: 15 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	c := &connections{db: db, ozonAccount: account, ozon: client, checks: map[string]map[string]connectionCheck{}}
	handler, err := handlerWithConnections(db, strings.Repeat("s", 32), "http://127.0.0.1:15175", c)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	var workerOnce sync.Once
	var workerWG sync.WaitGroup
	defer func() { gateOnce.Do(func() { close(gate) }); stopWorker(); workerWG.Wait() }()
	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	mux.HandleFunc("POST /__test/start-worker", func(w http.ResponseWriter, _ *http.Request) {
		workerOnce.Do(func() {
			workerWG.Add(1)
			go func() {
				defer workerWG.Done()
				w := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: ozonjob.Runner{Store: db, Account: account, Client: client}, ImportAccount: account, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 20 * time.Millisecond, Lease: 6 * time.Second, Retry: 100 * time.Millisecond}
				_ = w.Run(workerCtx)
			}()
		})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /__test/release", func(w http.ResponseWriter, _ *http.Request) {
		gateOnce.Do(func() { close(gate) })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /__test/state", func(w http.ResponseWriter, r *http.Request) {
		var operations, tasks, batches, products, warehouses, stock, linked, publication int
		err := db.Pool.QueryRow(r.Context(), `SELECT
		 (SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND account_id=$2),
		 (SELECT count(*) FROM outbox_tasks WHERE import_tenant_id=$1 AND import_account_id=$2),
		 (SELECT count(*) FROM source_imports WHERE source='ozon' AND account_id=$2),
		 (SELECT count(*) FROM ozon_product_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2),
		 (SELECT count(*) FROM ozon_warehouse_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2),
		 (SELECT count(*) FROM ozon_fbs_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2),
		 (SELECT count(*) FROM ozon_import_operations o JOIN ozon_import_batches b ON b.import_id=o.batch_id WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.outcome='COMPLETE_TRAVERSAL'),
		 (SELECT count(*) FROM outbox_tasks WHERE task_type NOT IN ('ozon_catalog_import','mock'))`, tenant, account).Scan(&operations, &tasks, &batches, &products, &warehouses, &stock, &linked, &publication)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		isStarted := false
		select {
		case <-started:
			isStarted = true
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"operations": operations, "tasks": tasks, "batches": batches, "products": products, "warehouses": warehouses, "stock": stock, "linked": linked, "publication": publication, "started": isStarted})
	})
	api := httptest.NewServer(mux)
	defer api.Close()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "import-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(config.Name())
	_, err = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15175,strictPort:true,proxy:{'/api':%q}}});`, api.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.Close(); err != nil {
		t.Fatal(err)
	}
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", config.Name())
	vite.Dir = web
	var log bytes.Buffer
	vite.Stdout, vite.Stderr = &log, &log
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vite.Process.Kill(); _ = vite.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		resp, e := http.Get("http://127.0.0.1:15175/")
		if e == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("Vite did not start")
	}
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/import-management.spec.ts", "--workers=1")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_IMPORT_E2E_URL="+api.URL, "STYK_IMPORT_E2E_TENANT="+tenant, "STYK_IMPORT_E2E_ACCOUNT="+account)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser E2E: %v\n%s", err, output)
	}
	t.Log(string(output))
	var operations, tasks, succeeded, cancelled, batches, products, warehouses, stocks, publication, otherEffects int
	err = db.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND account_id=$2),
	 (SELECT count(*) FROM outbox_tasks WHERE import_tenant_id=$1 AND import_account_id=$2),
	 (SELECT count(*) FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id JOIN ozon_import_batches b ON b.import_id=o.batch_id WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.outcome='COMPLETE_TRAVERSAL' AND t.state='succeeded'),
	 (SELECT count(*) FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.outcome='CANCELLED' AND t.state='cancelled'),
	 (SELECT count(*) FROM source_imports WHERE account_id=$2 AND source='ozon'),
	 (SELECT count(*) FROM ozon_product_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND p.product_id='1' AND p.offer_id='synthetic-offer'),
	 (SELECT count(*) FROM ozon_warehouse_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND p.warehouse_id='10' AND p.is_rfbs=false),
	 (SELECT count(*) FROM ozon_fbs_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND p.present=0 AND p.reserved=0 AND p.free_stock=0),
	 (SELECT count(*) FROM outbox_tasks WHERE task_type NOT IN ('ozon_catalog_import','mock')),
	 (SELECT count(*) FROM stock_pools WHERE tenant_id=$1) + (SELECT count(*) FROM stock_decisions WHERE tenant_id=$1) + (SELECT count(*) FROM product_mappings WHERE tenant_id=$1) + (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1)`, tenant, account).Scan(&operations, &tasks, &succeeded, &cancelled, &batches, &products, &warehouses, &stocks, &publication, &otherEffects)
	if err != nil || operations != 2 || tasks != 2 || succeeded != 1 || cancelled != 1 || batches != 1 || products != 1 || warehouses != 1 || stocks != 1 || publication != 0 || otherEffects != 0 {
		t.Fatalf("unexpected durable state: %d %d %d %d %d %d %d %d %d %d, %v", operations, tasks, succeeded, cancelled, batches, products, warehouses, stocks, publication, otherEffects, err)
	}
	callsMu.Lock()
	defer callsMu.Unlock()
	if len(calls) != 4 {
		t.Fatalf("expected four allowlisted read requests, got %v", calls)
	}
}
