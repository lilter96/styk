//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

// The browser, actual API, worker and PostgreSQL communicate only with local
// deterministic provider servers. No real credentials or provider hosts occur.
func TestControlledStockPublicationBrowser(t *testing.T) {
	if os.Getenv("STYK_CONTROLLED_BROWSER_E2E") != "1" {
		t.Skip("browser E2E not requested")
	}
	controlledStockBrowser(t, false, false)
}

func TestControlledStockRecoveryBrowser(t *testing.T) {
	if os.Getenv("STYK_CONTROLLED_RECOVERY_E2E") != "1" {
		t.Skip("recovery browser E2E not requested")
	}
	controlledStockBrowser(t, true, false)
}

func TestControlledStockAutomaticBrowser(t *testing.T) {
	if os.Getenv("STYK_CONTROLLED_AUTOMATIC_E2E") != "1" {
		t.Skip("automatic browser E2E not requested")
	}
	controlledStockBrowser(t, false, true)
}

func TestControlledStockAutomaticMultiBrowser(t *testing.T) {
	if os.Getenv("STYK_CONTROLLED_AUTOMATIC_MULTI_E2E") != "1" {
		t.Skip("automatic multi-product browser E2E not requested")
	}
	controlledStockBrowserMode(t, false, true, true)
}

func controlledStockBrowser(t *testing.T, recovery, automatic bool) {
	controlledStockBrowserMode(t, recovery, automatic, false)
}

func controlledStockBrowserMode(t *testing.T, recovery, automatic, multi bool) {
	var oldOperation string
	db, review := controlledFixtureWithMapping(t, recovery || automatic)
	scope := testpublication.Scope()
	if multi {
		seedMultiBrowserMapping(t, db)
	}
	if recovery {
		v := controlledEnable(t, db, review)
		oldOperation = v.LastOperation.ID
		now := time.Now().UTC()
		claim, err := db.NextControlledStockPublication(context.Background(), now)
		if err != nil || claim == nil {
			t.Fatalf("seed claim: %v", err)
		}
		if err = db.FinishStockTarget(context.Background(), scope, claim.ID, claim.Targets[0].ID, "UNKNOWN", "REQUEST_OUTCOME_UNKNOWN", now); err != nil {
			t.Fatal(err)
		}
		zero := int64(0)
		if err = db.RecordUnknownStockReadback(context.Background(), scope, claim.ID, claim.Targets[0].ID, &zero, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, err = db.MutateStockSync(context.Background(), scope, "disable", "", "synthetic-disable-recovery", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OZON_CLIENT_ID", scope.OzonAccount)
	t.Setenv("OZON_API_KEY", "synthetic-key")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", scope.MoyskladAccount)
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-token")
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	var stock, secondStock, writes atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			var body bytes.Buffer
			_, _ = io.Copy(&body, r.Body)
			if bytes.Contains(body.Bytes(), []byte(`"ARCHIVED"`)) {
				fmt.Fprint(w, `{"result":{"items":[],"total_items":0,"last_id":""}}`)
			} else if multi {
				fmt.Fprint(w, `{"result":{"items":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"archived":false},{"product_id":2764939447,"offer_id":"ATEST-0010987","sku":2889575995,"archived":false}],"total_items":2,"last_id":""}}`)
			} else {
				fmt.Fprint(w, `{"result":{"items":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"archived":false}],"total_items":1,"last_id":""}}`)
			}
		case "/v2/warehouse/list":
			fmt.Fprint(w, `{"warehouses":[{"warehouse_id":1020001323428000,"name":"Тестовый FBS","status":"created","is_rfbs":false}],"has_next":false,"cursor":""}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			if multi {
				fmt.Fprintf(w, `{"products":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"warehouse_id":1020001323428000,"present":%d,"reserved":0,"free_stock":%d},{"product_id":2764939447,"offer_id":"ATEST-0010987","sku":2889575995,"warehouse_id":1020001323428000,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, stock.Load(), stock.Load(), secondStock.Load(), secondStock.Load())
			} else {
				fmt.Fprintf(w, `{"products":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"warehouse_id":1020001323428000,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, stock.Load(), stock.Load())
			}
		case "/v2/products/stocks":
			writes.Add(1)
			if multi {
				var request struct {
					Stocks []struct {
						ProductID int64 `json:"product_id"`
						Stock     int32 `json:"stock"`
					} `json:"stocks"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Stocks) != 1 {
					t.Error("invalid test write")
					http.Error(w, "bad", 400)
					return
				}
				p := request.Stocks[0]
				switch p.ProductID {
				case 2764544165:
					stock.Store(p.Stock)
				case 2764939447:
					secondStock.Store(p.Stock)
				default:
					t.Error("wrong test product")
					http.Error(w, "bad", 400)
					return
				}
				fmt.Fprintf(w, `{"result":[{"product_id":%d,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`, p.ProductID)
			} else {
				stock.Store(7)
				fmt.Fprint(w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
			}
		case "/entity/product":
			if multi {
				fmt.Fprintf(w, `{"meta":{"size":2,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Первый товар"},{"id":"ea33e770-d389-11f1-0a80-0d480029c772","name":"Товар с резервом"}]}`, testpublication.SourceProduct)
			} else {
				fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Тестовый товар","article":"synthetic"}]}`, testpublication.SourceProduct)
			}
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`)
		case "/entity/store":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Тестовый склад"}]}`, testpublication.SourceWarehouse)
		case "/report/stock/all":
			if multi {
				fmt.Fprintf(w, `{"meta":{"size":2,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stock":4,"reserve":0,"inTransit":0,"quantity":4},{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/ea33e770-d389-11f1-0a80-0d480029c772","type":"product"},"stock":10,"reserve":3,"inTransit":0,"quantity":7}]}`, testpublication.SourceProduct)
			} else {
				fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stock":7,"reserve":0,"inTransit":0,"quantity":7}]}`, testpublication.SourceProduct)
			}
		case "/report/stock/bystore":
			if multi {
				fmt.Fprintf(w, `{"meta":{"size":2,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":4,"reserve":0,"inTransit":0}]},{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/ea33e770-d389-11f1-0a80-0d480029c772","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":10,"reserve":3,"inTransit":0}]}]}`, testpublication.SourceProduct, testpublication.SourceWarehouse, testpublication.SourceWarehouse)
			} else {
				fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":7,"reserve":0,"inTransit":0}]}]}`, testpublication.SourceProduct, testpublication.SourceWarehouse)
			}
		default:
			t.Errorf("unexpected synthetic provider path %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	oz, err := ozon.New(ozon.Options{BaseURL: provider.URL, ClientID: scope.OzonAccount, APIKey: "synthetic-key", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	mo, err := moysklad.New(moysklad.Options{BaseURL: provider.URL, Token: "synthetic-token", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	pub := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return oz, nil }}
	imports := ozonjob.Runner{Store: db, Account: scope.OzonAccount, Client: oz, MoyskladAccount: scope.MoyskladAccount, MoyskladClient: mo}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	worker := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: imports, Publications: pub, ImportAccount: scope.OzonAccount, MoyskladImportAccount: scope.MoyskladAccount, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 50 * time.Millisecond, Lease: time.Minute, Retry: time.Second}
	if automatic {
		worker.Automatic = stocksync.AutomaticRunner{Store: db, Publisher: pub}
	}
	done := make(chan error, 1)
	var once sync.Once
	var started atomic.Bool
	startWorker := func() { once.Do(func() { started.Store(true); go func() { done <- worker.Run(ctx) }() }) }
	if !recovery {
		startWorker()
	}
	defer func() {
		cancel()
		if started.Load() {
			<-done
		}
	}()
	const origin = "http://127.0.0.1:15178"
	const secret = "synthetic-controlled-browser-operator-secret"
	handler, err := console.Handler(db, secret, origin)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /__test/start-worker", func(w http.ResponseWriter, _ *http.Request) { startWorker(); w.WriteHeader(http.StatusNoContent) })
	mux.Handle("/", handler)
	api := httptest.NewServer(mux)
	defer api.Close()
	web, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.CreateTemp(filepath.Join(web, "node_modules/.cache"), "controlled-vite-*.mjs")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(config.Name())
	_, _ = fmt.Fprintf(config, `import {defineConfig} from 'vite'; export default defineConfig({server:{host:'127.0.0.1',port:15178,strictPort:true,proxy:{'/api':%q}}});`, api.URL)
	_ = config.Close()
	vite := exec.CommandContext(ctx, "node", "node_modules/vite/bin/vite.js", "--config", config.Name())
	vite.Dir = web
	var logs bytes.Buffer
	vite.Stdout = &logs
	vite.Stderr = &logs
	if err = vite.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vite.Process.Kill(); _ = vite.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		resp, e := http.Get(origin)
		if e == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("vite: %s", logs.String())
	}
	spec := "e2e/controlled-stock.spec.ts"
	if recovery {
		spec = "e2e/controlled-recovery.spec.ts"
	}
	if automatic {
		spec = "e2e/controlled-automatic.spec.ts"
	}
	if multi {
		spec = "e2e/controlled-automatic-multi.spec.ts"
	}
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", spec, "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_CONTROLLED_E2E_ORIGIN="+origin, "STYK_CONTROLLED_E2E_SECRET="+secret, "STYK_CONTROLLED_E2E_TEST_API="+api.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled browser: %v\n%s", err, output)
	}
	t.Log(string(output))
	if (!multi && (writes.Load() != 1 || stock.Load() != 7)) || (multi && (writes.Load() != 2 || stock.Load() != 4 || secondStock.Load() != 7)) {
		t.Fatalf("fake provider writes=%d stock=%d", writes.Load(), stock.Load())
	}
	var tasks int
	expected := 1
	if multi {
		expected = 2
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks WHERE task_type='stock_publication' AND state='succeeded' AND attempts=1`).Scan(&tasks); err != nil || tasks != expected {
		t.Fatalf("outbox count %d: %v", tasks, err)
	}
	var productAudits, warehouseAudits int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_mapping_confirmation_audits WHERE tenant_id=$1 AND evidence_class='OPERATOR_ATTESTED'), (SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id=$1 AND evidence_class='OPERATOR_ATTESTED')`, scope.Tenant).Scan(&productAudits, &warehouseAudits); err != nil || productAudits != expected || warehouseAudits != 1 {
		t.Fatalf("browser mapping audits product=%d warehouse=%d: %v", productAudits, warehouseAudits, err)
	}
	if recovery {
		var oldState, linked string
		if err = db.Pool.QueryRow(ctx, `SELECT t.state,l.new_operation_id FROM stock_publication_targets t JOIN stock_publication_recoveries r ON r.target_id=t.id JOIN stock_publication_recovery_links l ON l.recovery_id=r.id WHERE t.operation_id=$1`, oldOperation).Scan(&oldState, &linked); err != nil || oldState != "UNKNOWN" || linked == oldOperation || linked == "" {
			t.Fatalf("recovery audit original=%s new=%s: %v", oldState, linked, err)
		}
	}
}

func seedMultiBrowserMapping(t *testing.T, db *postgres.Store) {
	t.Helper()
	scope := testpublication.Scope()
	now := time.Now().UTC().Add(-time.Second)
	zero, four, ten, three := "0", "4", "10", "3"
	fbs, archived := false, false
	const item = "ea33e770-d389-11f1-0a80-0d480029c772"
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now.Add(time.Millisecond), Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct}, {Kind: "product", ID: item}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &four, Reserve: &zero, InTransit: &zero}, {Kind: "product", ExternalID: item, WarehouseID: testpublication.SourceWarehouse, Stock: &ten, Reserve: &three, InTransit: &zero}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now.Add(time.Millisecond), Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}, {ProductID: "2764939447", OfferID: "ATEST-0010987", SKU: "2889575995", Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, Status: "created", IsRFBS: &fbs}}, Stocks: []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, WarehouseID: testpublication.OzonWarehouse}, {ProductID: "2764939447", OfferID: "ATEST-0010987", SKU: "2889575995", WarehouseID: testpublication.OzonWarehouse}}}
	ctx := context.Background()
	if _, err := db.SaveInventoryImport(ctx, source, "synthetic-browser-multi-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "synthetic-browser-multi-market"); err != nil {
		t.Fatal(err)
	}
	r, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, ProductID: "2764939447", TargetKind: "product", TargetID: item, Evidence: domain.MappingOperatorAttested, SessionID: "synthetic", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: r.Review.ID, SessionID: "synthetic", IdempotencyKey: "browser-multi-mapping", Acknowledged: true, Explanation: "Synthetic browser reservation mapping"}); err != nil {
		t.Fatal(err)
	}
}
