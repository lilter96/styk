//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestAutomaticInitialStockRequiresExplicitSingleTakeover(t *testing.T) {
	db, _ := controlledFixture(t)
	scope := testpublication.Scope()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var row atomic.Bool
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			var in struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Filter.Visibility == "ARCHIVED" {
				fmt.Fprint(w, `{"result":{"items":[],"total_items":0,"last_id":""}}`)
				return
			}
			fmt.Fprint(w, `{"result":{"items":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"archived":false}],"total_items":1,"last_id":""}}`)
		case "/v2/warehouse/list":
			fmt.Fprint(w, `{"warehouses":[{"warehouse_id":1020001323428000,"name":"FBS","status":"created","is_rfbs":false}],"has_next":false,"cursor":""}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			if row.Load() {
				fmt.Fprint(w, `{"products":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"warehouse_id":1020001323428000,"present":7,"reserved":0,"free_stock":7}],"has_next":false,"cursor":""}`)
			} else {
				fmt.Fprint(w, `{"products":[],"has_next":false,"cursor":""}`)
			}
		case "/v2/products/stocks":
			var in struct {
				Stocks []struct {
					ProductID   int64 `json:"product_id"`
					WarehouseID int64 `json:"warehouse_id"`
					Stock       int64 `json:"stock"`
				} `json:"stocks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Stocks) != 1 || in.Stocks[0].ProductID != 2764544165 || in.Stocks[0].WarehouseID != 1020001323428000 || in.Stocks[0].Stock != 7 {
				t.Errorf("unexpected first stock request: %+v %v", in, err)
				http.Error(w, "invalid", 400)
				return
			}
			writes.Add(1)
			row.Store(true)
			fmt.Fprint(w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
		case "/entity/product":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Test","article":"synthetic"}]}`, testpublication.SourceProduct)
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`)
		case "/entity/store":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Test"}]}`, testpublication.SourceWarehouse)
		case "/report/stock/all":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stock":7,"reserve":0,"inTransit":0,"quantity":7}]}`, testpublication.SourceProduct)
		case "/report/stock/bystore":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":7,"reserve":0,"inTransit":0}]}]}`, testpublication.SourceProduct, testpublication.SourceWarehouse)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	oz, err := ozon.New(ozon.Options{BaseURL: srv.URL, ClientID: scope.OzonAccount, APIKey: "synthetic", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	mo, err := moysklad.New(moysklad.Options{BaseURL: srv.URL, Token: "synthetic", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	pub := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return oz, nil }}
	imports := ozonjob.Runner{Store: db, Account: scope.OzonAccount, Client: oz, MoyskladAccount: scope.MoyskladAccount, MoyskladClient: mo}
	worker := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: imports, Publications: pub, Automatic: stocksync.AutomaticRunner{Store: db, Publisher: pub}, ImportAccount: scope.OzonAccount, MoyskladImportAccount: scope.MoyskladAccount, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 50 * time.Millisecond, Lease: time.Minute, Retry: time.Second}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	req := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, Targets: []domain.AutomaticStockTargetRequest{{ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7}}, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "initial-policy-without-authorization"}
	if _, err = db.ConfigureAutomaticStock(ctx, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	wait := func(previous, state string) domain.AutomaticStockView {
		t.Helper()
		for i := 0; i < 300; i++ {
			v, e := db.AutomaticStockStatus(ctx, scope)
			if e != nil {
				t.Fatal(e)
			}
			if v.LastCycle != nil && v.LastCycle.ID != previous && v.LastCycle.State == state {
				return v
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		t.Fatal("cycle timed out")
		return domain.AutomaticStockView{}
	}
	blocked := wait("", "BLOCKED")
	if writes.Load() != 0 || len(blocked.Candidates) != 1 || blocked.Candidates[0].MarketplaceFree != nil || !blocked.Candidates[0].InitialEligible {
		t.Fatalf("missing observation not blocked: %+v writes=%d", blocked, writes.Load())
	}
	req.Targets[0].InitialPublication = true
	req.IdempotencyKey = "initial-policy-explicit-authorization"
	if _, err = db.ConfigureAutomaticStock(ctx, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, _ = db.AdvanceAutomaticStock(ctx, time.Now()) }()
	}
	wg.Wait()
	completed := wait(blocked.LastCycle.ID, "COMPLETE")
	if completed.LastCycle.Outcome != "RECONCILED" || writes.Load() != 1 {
		t.Fatalf("first publication: %+v writes=%d", completed.LastCycle, writes.Load())
	}
	op, err := db.StockPublication(ctx, scope, completed.LastCycle.PublicationID)
	if err != nil || op.State != "RECONCILED" || len(op.Targets) != 1 || op.Targets[0].ReadbackQuantity == nil || *op.Targets[0].ReadbackQuantity != "7" || op.Targets[0].Decision.Evidence.Marketplace.StockRowFound || op.Targets[0].Decision.Evidence.Marketplace.FreeStock != nil {
		t.Fatalf("first observation was fabricated or not reconciled: %+v %v", op, err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_initial_publications WHERE publication_id=$1 AND config_revision=$2`, op.ID, completed.Policy.Revision).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing one-shot audit: %d %v", count, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE stock_initial_publications SET product_id='forged' WHERE publication_id=$1`, op.ID); err == nil {
		t.Fatal("initial authorization was mutable")
	}
	row.Store(false)
	if _, err = db.RequestAutomaticCycle(ctx, scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	again := wait(completed.LastCycle.ID, "BLOCKED")
	if writes.Load() != 1 || again.Candidates[0].MarketplaceFree != nil || again.Candidates[0].InitialEligible {
		t.Fatalf("missing row triggered repeated takeover: %+v writes=%d", again, writes.Load())
	}
}
