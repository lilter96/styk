//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

// The browser, actual API, worker and PostgreSQL communicate only with local
// deterministic provider servers. No real credentials or provider hosts occur.
func TestControlledStockPublicationBrowser(t *testing.T) {
	if os.Getenv("STYK_CONTROLLED_BROWSER_E2E") != "1" {
		t.Skip("browser E2E not requested")
	}
	db, _ := controlledFixture(t)
	scope := testpublication.Scope()
	t.Setenv("OZON_CLIENT_ID", scope.OzonAccount)
	t.Setenv("OZON_API_KEY", "synthetic-key")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", scope.MoyskladAccount)
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-token")
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	var stock, writes atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			var body bytes.Buffer
			_, _ = io.Copy(&body, r.Body)
			if bytes.Contains(body.Bytes(), []byte(`"ARCHIVED"`)) {
				fmt.Fprint(w, `{"result":{"items":[],"total_items":0,"last_id":""}}`)
			} else {
				fmt.Fprint(w, `{"result":{"items":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"archived":false}],"total_items":1,"last_id":""}}`)
			}
		case "/v2/warehouse/list":
			fmt.Fprint(w, `{"warehouses":[{"warehouse_id":1020001323428000,"name":"Тестовый FBS","status":"created","is_rfbs":false}],"has_next":false,"cursor":""}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			fmt.Fprintf(w, `{"products":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"warehouse_id":1020001323428000,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, stock.Load(), stock.Load())
		case "/v2/products/stocks":
			writes.Add(1)
			stock.Store(7)
			fmt.Fprint(w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
		case "/entity/product":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Тестовый товар","article":"synthetic"}]}`, testpublication.SourceProduct)
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`)
		case "/entity/store":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Тестовый склад"}]}`, testpublication.SourceWarehouse)
		case "/report/stock/all":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stock":7,"reserve":0,"inTransit":0,"quantity":7}]}`, testpublication.SourceProduct)
		case "/report/stock/bystore":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":7,"reserve":0,"inTransit":0}]}]}`, testpublication.SourceProduct, testpublication.SourceWarehouse)
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
	pub := stocksync.Runner{Enabled: true, ControlledTest: true, Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return oz, nil }}
	imports := ozonjob.Runner{Store: db, Account: scope.OzonAccount, Client: oz, MoyskladAccount: scope.MoyskladAccount, MoyskladClient: mo}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	worker := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: imports, Publications: pub, ImportAccount: scope.OzonAccount, MoyskladImportAccount: scope.MoyskladAccount, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 50 * time.Millisecond, Lease: time.Minute, Retry: time.Second}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	const origin = "http://127.0.0.1:15178"
	const secret = "synthetic-controlled-browser-operator-secret"
	handler, err := console.Handler(db, secret, origin)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(handler)
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
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "e2e/controlled-stock.spec.ts", "--workers=1", "--reporter=line")
	cmd.Dir = web
	cmd.Env = append(os.Environ(), "STYK_CONTROLLED_E2E_ORIGIN="+origin, "STYK_CONTROLLED_E2E_SECRET="+secret)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled browser: %v\n%s", err, output)
	}
	t.Log(string(output))
	if writes.Load() != 1 || stock.Load() != 7 {
		t.Fatalf("fake provider writes=%d stock=%d", writes.Load(), stock.Load())
	}
	var tasks int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks WHERE task_type='stock_publication' AND state='succeeded' AND attempts=1`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("outbox count %d: %v", tasks, err)
	}
}
