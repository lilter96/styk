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
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
)

// TestImportManagementBrowser uses the actual API, outbox worker and importer,
// but only a synthetic, explicitly allowlisted HTTP server and disposable DB.
func TestImportManagementBrowser(t *testing.T) {
	for _, provider := range []string{"ozon", "moysklad"} {
		t.Run(provider, func(t *testing.T) { testProviderImportBrowser(t, provider) })
	}
}
func testProviderImportBrowser(t *testing.T, provider string) {
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
	tenant, account := "browser-import-"+suffix, "browser-account-"+suffix
	t.Setenv("OZON_CLIENT_ID", account)
	t.Setenv("OZON_API_KEY", "synthetic-import-key")
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-moy-import-token")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", account)
	if err := db.RegisterStockConnection(ctx, tenant, "ozon", account, "synthetic browser fixture", false); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterStockConnection(ctx, tenant, "moysklad", account, "synthetic browser fixture", false); err != nil {
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

		if strings.HasPrefix(r.URL.Path, "/entity/") || strings.HasPrefix(r.URL.Path, "/report/") {
			if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-moy-import-token" {
				t.Error("unexpected MoySklad operation")
				w.WriteHeader(405)
				return
			}
			var rows string
			switch r.URL.Path {
			case "/entity/product":
				startedOnce.Do(func() { close(started) })
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
				rows = fmt.Sprintf(`[{"id":"p1","accountId":%q,"name":"Synthetic product","article":"synthetic","updated":"2026-01-01 10:00:00.000"}]`, account)
			case "/entity/variant":
				rows = fmt.Sprintf(`[{"id":"v1","accountId":%q,"name":"Synthetic variant","product":{"meta":{"href":"/entity/product/p1","type":"product"}}}]`, account)
			case "/entity/store":
				rows = fmt.Sprintf(`[{"id":"w1","accountId":%q,"name":"Synthetic warehouse"}]`, account)
			case "/report/stock/all":
				rows = `[{"meta":{"href":"/entity/product/p1","type":"product"},"stock":0,"reserve":0,"inTransit":0,"quantity":0}]`
			case "/report/stock/bystore":
				rows = `[{"meta":{"href":"/entity/variant/v1","type":"variant"},"stockByStore":[{"meta":{"href":"/entity/store/w1","type":"store"},"stock":5,"reserve":1,"inTransit":2}]}]`
			default:
				t.Error("unallowlisted MoySklad endpoint")
				w.WriteHeader(405)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":%s}`, rows)
			return
		}
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
	mc, err := moysklad.New(moysklad.Options{BaseURL: mock.URL + "/", Token: "synthetic-moy-import-token", Timeout: 15 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	c := &connections{db: db, moyAccount: account, moy: mc, ozonAccount: account, ozon: client, checks: map[string]map[string]connectionCheck{}}
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
				w := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: ozonjob.Runner{Store: db, Account: account, Client: client, MoyskladAccount: account, MoyskladClient: mc}, ImportAccount: account, MoyskladImportAccount: account, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 20 * time.Millisecond, Lease: 6 * time.Second, Retry: 100 * time.Millisecond}
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
		 (SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND account_id=$2 AND provider=$3),
		 (SELECT count(*) FROM outbox_tasks WHERE import_tenant_id=$1 AND import_account_id=$2 AND import_provider=$3),
		 (SELECT count(*) FROM source_imports WHERE source=$3 AND account_id=$2),
		 (CASE WHEN $3='ozon' THEN (SELECT count(*) FROM ozon_product_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2) ELSE (SELECT count(*) FROM source_entities p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND s.source=$3 AND p.kind='product') END),
		 (CASE WHEN $3='ozon' THEN (SELECT count(*) FROM ozon_warehouse_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2) ELSE (SELECT count(*) FROM source_entities p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND s.source=$3 AND p.kind='store') END),
		 (CASE WHEN $3='ozon' THEN (SELECT count(*) FROM ozon_fbs_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2) ELSE (SELECT count(*) FROM source_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND s.source=$3) END),
		 (SELECT count(*) FROM ozon_import_operations o JOIN source_imports b ON b.id=o.batch_id AND b.source=o.provider WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.provider=$3 AND o.outcome='COMPLETE_TRAVERSAL'),
		 (SELECT count(*) FROM outbox_tasks WHERE task_type NOT IN ('ozon_catalog_import','moysklad_catalog_import','mock'))`, tenant, account, provider).Scan(&operations, &tasks, &batches, &products, &warehouses, &stock, &linked, &publication)
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
	cmd.Env = append(os.Environ(), "STYK_IMPORT_E2E_PROVIDER="+provider, "STYK_IMPORT_E2E_URL="+api.URL, "STYK_IMPORT_E2E_TENANT="+tenant, "STYK_IMPORT_E2E_ACCOUNT="+account)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser E2E: %v\n%s", err, output)
	}
	t.Log(string(output))
	var operations, tasks, succeeded, cancelled, batches, products, warehouses, stocks, publication, otherEffects int
	err = db.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND account_id=$2 AND provider=$3),
	 (SELECT count(*) FROM outbox_tasks WHERE import_tenant_id=$1 AND import_account_id=$2 AND import_provider=$3),
	 (SELECT count(*) FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id JOIN source_imports b ON b.id=o.batch_id AND b.source=o.provider WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.provider=$3 AND o.outcome='COMPLETE_TRAVERSAL' AND t.state='succeeded'),
	 (SELECT count(*) FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.provider=$3 AND o.outcome='CANCELLED' AND t.state='cancelled'),
	 (SELECT count(*) FROM source_imports WHERE account_id=$2 AND source=$3),
	 (CASE WHEN $3='ozon' THEN (SELECT count(*) FROM ozon_product_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND p.product_id='1' AND p.offer_id='synthetic-offer') ELSE (SELECT count(*) FROM source_entities p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND s.source=$3 AND p.kind='product' AND p.external_id='p1') END),
	 (CASE WHEN $3='ozon' THEN (SELECT count(*) FROM ozon_warehouse_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND p.warehouse_id='10' AND p.is_rfbs=false) ELSE (SELECT count(*) FROM source_entities p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND s.source=$3 AND p.kind='store' AND p.external_id='w1') END),
	 (CASE WHEN $3='ozon' THEN (SELECT count(*) FROM ozon_fbs_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND p.present=0 AND p.reserved=0 AND p.free_stock=0) ELSE (SELECT count(*) FROM source_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.account_id=$2 AND s.source=$3 AND p.stock=0 AND p.reserve=0 AND p.in_transit=0 AND p.quantity=0) END),
	 (SELECT count(*) FROM outbox_tasks WHERE task_type NOT IN ('ozon_catalog_import','moysklad_catalog_import','mock')),
	 (SELECT count(*) FROM stock_pools WHERE tenant_id=$1) + (SELECT count(*) FROM stock_decisions WHERE tenant_id=$1) + (SELECT count(*) FROM product_mappings WHERE tenant_id=$1) + (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1)`, tenant, account, provider).Scan(&operations, &tasks, &succeeded, &cancelled, &batches, &products, &warehouses, &stocks, &publication, &otherEffects)
	if err != nil || operations != 2 || tasks != 2 || succeeded != 1 || cancelled != 1 || batches != 1 || products != 1 || warehouses != 1 || stocks != 1 || publication != 0 || otherEffects != 0 {
		t.Fatalf("unexpected durable state: %d %d %d %d %d %d %d %d %d %d, %v", operations, tasks, succeeded, cancelled, batches, products, warehouses, stocks, publication, otherEffects, err)
	}
	if provider == "moysklad" {
		var variants, nullable int
		err = db.Pool.QueryRow(ctx, `SELECT
   (SELECT count(*) FROM source_entities p JOIN source_imports s ON s.id=p.import_id WHERE s.source='moysklad' AND s.account_id=$1 AND p.kind='variant' AND p.external_id='v1' AND p.parent_id='p1'),
   (SELECT count(*) FROM source_warehouse_stock_observations p JOIN source_imports s ON s.id=p.import_id WHERE s.source='moysklad' AND s.account_id=$1 AND p.kind='variant' AND p.external_id='v1' AND p.warehouse_id='w1' AND p.stock=5 AND p.reserve=1 AND p.in_transit=2 AND p.quantity IS NULL AND p.observed_at=s.finished_at)`, account).Scan(&variants, &nullable)
		if err != nil || variants != 1 || nullable != 1 {
			t.Fatal("MoySklad variant/stock semantics lost", err)
		}
	}
	var both int
	if err = db.Pool.QueryRow(ctx, `SELECT count(DISTINCT o.provider) FROM ozon_import_operations o JOIN source_imports s ON s.id=o.batch_id AND s.source=o.provider AND s.account_id=o.account_id JOIN outbox_tasks t ON t.import_operation_id=o.id AND t.import_provider=o.provider WHERE o.tenant_id=$1 AND o.account_id=$2 AND t.state='succeeded' AND o.outcome='COMPLETE_TRAVERSAL'`, tenant, account).Scan(&both); err != nil || both != 2 {
		t.Fatal("shared worker did not persist both providers", err)
	}
	callsMu.Lock()
	defer callsMu.Unlock()
	// Both types execute on the same worker lifetime: four Ozon reads and five MoySklad reads.
	wantCalls := 9
	if len(calls) != wantCalls {
		t.Fatalf("unexpected allowlisted read request count, got %v", calls)
	}
}
