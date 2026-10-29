//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
)

type automaticMultiFixture struct {
	scope   domain.StockSyncScope
	source  domain.InventoryImport
	market  domain.OzonImport
	targets []domain.AutomaticStockTargetRequest
}

// The isolated-schema test removes only the live-account CHECK in that test
// schema. The normal migrated database retains it and the simulator uses an
// HTTP loopback client; no real target or credential can be dispatched here.
func automaticMultiScope(t *testing.T, db *postgres.Store, suffix string, multiple bool) automaticMultiFixture {
	t.Helper()
	ctx := context.Background()
	scope := domain.StockSyncScope{Tenant: "synthetic-auto-" + suffix, MoyskladAccount: "synthetic-moy-" + suffix, OzonAccount: "synthetic-ozon-" + suffix}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic local ownership", true); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Add(-time.Minute)
	zero, incoming, stock5, stock4, stock2 := "0", "0", "5", "4", "2"
	fbs, archived := false, false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "item-a"}, {Kind: "store", ID: "store-a"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: "item-a", WarehouseID: "store-a", Stock: &stock5, Reserve: &zero, InTransit: &incoming}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "101", OfferID: "offer-101", SKU: "1001", Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: "201", Status: "created", IsRFBS: &fbs}}, Stocks: []domain.OzonStock{{ProductID: "101", OfferID: "offer-101", SKU: "1001", WarehouseID: "201", Present: 0, Reserved: 0, FreeStock: 0}}}
	type pair struct{ product, kind, item, warehouse string }
	pairs := []pair{{"101", "product", "item-a", "201"}}
	if multiple {
		source.Entities = append(source.Entities, domain.ExternalEntity{Kind: "store", ID: "store-b"}, domain.ExternalEntity{Kind: "product", ID: "parent-b"}, domain.ExternalEntity{Kind: "variant", ID: "item-b", ParentID: "parent-b"}, domain.ExternalEntity{Kind: "product", ID: "parent-c"}, domain.ExternalEntity{Kind: "variant", ID: "item-c", ParentID: "parent-c"})
		source.WarehouseStock = append(source.WarehouseStock, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "item-b", WarehouseID: "store-b", Stock: &stock4, Reserve: &zero, InTransit: &incoming}, domain.WarehouseStockObservation{Kind: "product", ExternalID: "parent-c", WarehouseID: "store-a", Stock: &stock2, Reserve: &zero, InTransit: &incoming}, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "item-c", WarehouseID: "store-a", Stock: &stock2, Reserve: &zero, InTransit: &incoming})
		market.Products = append(market.Products, domain.OzonProduct{ProductID: "102", OfferID: "offer-102", SKU: "1002", Archived: &archived}, domain.OzonProduct{ProductID: "103", OfferID: "offer-103", SKU: "1003", Archived: &archived}, domain.OzonProduct{ProductID: "104", OfferID: "offer-104", SKU: "1004", Archived: &archived})
		market.Warehouses = append(market.Warehouses, domain.OzonWarehouse{ID: "202", Status: "created", IsRFBS: &fbs})
		market.Stocks = append(market.Stocks, domain.OzonStock{ProductID: "102", OfferID: "offer-102", SKU: "1002", WarehouseID: "202", Present: 0, Reserved: 0, FreeStock: 0}, domain.OzonStock{ProductID: "103", OfferID: "offer-103", SKU: "1003", WarehouseID: "201", Present: 0, Reserved: 0, FreeStock: 0}, domain.OzonStock{ProductID: "104", OfferID: "offer-104", SKU: "1004", WarehouseID: "201", Present: 0, Reserved: 0, FreeStock: 0})
		pairs = append(pairs, pair{"102", "variant", "item-b", "202"}, pair{"103", "product", "parent-c", "201"}, pair{"104", "variant", "item-c", "201"})
	}
	if _, err := db.SaveInventoryImport(ctx, source, "synthetic-multi-source-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "synthetic-multi-market-"+suffix); err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		r, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, ProductID: p.product, TargetKind: p.kind, TargetID: p.item, Evidence: domain.MappingOperatorAttested, SessionID: "synthetic", Lifetime: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: r.Review.ID, SessionID: "synthetic", IdempotencyKey: "product-" + suffix + "-" + p.product, Acknowledged: true, Explanation: "Synthetic independent mapping fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	warehouses := [][2]string{{"store-a", "201"}}
	if multiple {
		warehouses = append(warehouses, [2]string{"store-b", "202"})
	}
	for _, w := range warehouses {
		r, err := db.CreateWarehouseReview(ctx, domain.WarehouseReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, MoyskladWarehouseID: w[0], OzonWarehouseID: w[1], SessionID: "synthetic", Lifetime: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ConfirmWarehouseReview(ctx, domain.MappingConfirmationRequest{ReviewID: r.Review.ID, SessionID: "synthetic", IdempotencyKey: "warehouse-" + suffix + "-" + w[1], Acknowledged: true, Explanation: "Synthetic warehouse pair"}); err != nil {
			t.Fatal(err)
		}
	}
	inputs := make([]domain.AutomaticStockTargetRequest, 0, len(pairs))
	for _, p := range pairs {
		inputs = append(inputs, domain.AutomaticStockTargetRequest{ProductID: p.product, WarehouseID: p.warehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7})
	}
	_, err := db.ConfigureAutomaticStock(ctx, domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, Targets: inputs, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "synthetic-policy-" + suffix}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return automaticMultiFixture{scope: scope, source: source, market: market, targets: inputs}
}

func TestAutomaticMultipleTargetsScopesAndUnknownIsolation(t *testing.T) {
	db, _ := controlledFixture(t)
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE stock_auto_configs DROP CONSTRAINT stock_auto_configs_check`); err != nil {
		t.Fatal(err)
	}
	a := automaticMultiScope(t, db, "one", true)
	var writes atomic.Int32
	var mu sync.Mutex
	market := map[string]int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account := r.Header.Get("Client-Id")
		switch r.URL.Path {
		case "/v2/products/stocks":
			var in struct {
				Stocks []struct {
					ProductID   int64 `json:"product_id"`
					WarehouseID int64 `json:"warehouse_id"`
					Stock       int64 `json:"stock"`
				} `json:"stocks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Stocks) != 1 {
				t.Error("invalid batch")
				http.Error(w, "bad", 400)
				return
			}
			x := in.Stocks[0]
			writes.Add(1)
			if account == a.scope.OzonAccount && x.ProductID == 101 {
				http.Error(w, "uncertain", 500)
				return
			}
			mu.Lock()
			market[fmt.Sprintf("%s:%d:%d", account, x.ProductID, x.WarehouseID)] = x.Stock
			mu.Unlock()
			fmt.Fprintf(w, `{"result":[{"product_id":%d,"warehouse_id":%d,"updated":true,"errors":[]}]}`, x.ProductID, x.WarehouseID)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			var in struct {
				OfferID []string `json:"offer_id"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.OfferID) != 1 {
				http.Error(w, "bad", 400)
				return
			}
			var product, warehouse int64
			switch in.OfferID[0] {
			case "offer-101":
				product, warehouse = 101, 201
			case "offer-102":
				product, warehouse = 102, 202
			default:
				t.Error("unexpected offer")
				http.Error(w, "bad", 400)
				return
			}
			mu.Lock()
			value := market[fmt.Sprintf("%s:%d:%d", account, product, warehouse)]
			mu.Unlock()
			fmt.Fprintf(w, `{"products":[{"product_id":%d,"warehouse_id":%d,"offer_id":%q,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, product, warehouse, in.OfferID[0], value, value)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "bad", 500)
		}
	}))
	defer server.Close()
	clients := map[string]*ozon.Client{}
	makeClient := func(account string) *ozon.Client {
		if c := clients[account]; c != nil {
			return c
		}
		c, e := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: account, APIKey: "synthetic", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
		if e != nil {
			t.Fatal(e)
		}
		clients[account] = c
		return c
	}
	runner := stocksync.Runner{Enabled: true, Store: db, ResolveOzon: func(_ context.Context, _, account string) (*ozon.Client, error) { return makeClient(account), nil }}
	advance := func() {
		t.Helper()
		if _, _, err := db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	startReads := func(f automaticMultiFixture, previous string) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			advance()
			v, err := db.AutomaticStockStatus(ctx, f.scope)
			if err != nil {
				t.Fatal(err)
			}
			if v.LastCycle != nil && v.LastCycle.ID != previous && v.LastCycle.State == "READING" {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		v, err := db.AutomaticStockStatus(ctx, f.scope)
		if err != nil || v.LastCycle == nil || v.LastCycle.ID == previous || v.LastCycle.State != "READING" {
			t.Fatalf("cycle did not schedule reads: %+v %v", v, err)
		}
		for i := 0; i < 2; i++ {
			task, e := db.ClaimForCatalogWorker(ctx, time.Minute, f.scope.OzonAccount, f.scope.MoyskladAccount)
			if e != nil || task == nil {
				t.Fatalf("claim import: %+v %v", task, e)
			}
			now := time.Now().UTC()
			counts := domain.ImportCounts{Attempts: 1, Requests: 1, CompletedRequests: 1}
			if task.Type == domain.MoyskladCatalogTask {
				snap := f.source
				snap.StartedAt = now
				snap.FinishedAt = now
				fingerprint, e := application.InventoryFingerprint(&snap)
				if e != nil {
					t.Fatal(e)
				}
				if e = db.CommitClaimedMoyskladImport(ctx, *task, snap, fingerprint, counts, 5*time.Second); e != nil {
					t.Fatal(e)
				}
			} else if task.Type == domain.OzonCatalogTask {
				snap := f.market
				snap.StartedAt = now
				snap.FinishedAt = now
				fingerprint, e := application.OzonFingerprint(&snap)
				if e != nil {
					t.Fatal(e)
				}
				if e = db.CommitClaimedOzonImport(ctx, *task, snap, fingerprint, counts, 5*time.Second); e != nil {
					t.Fatal(e)
				}
			} else {
				t.Fatalf("unexpected task: %+v", task)
			}
		}
	}
	waitState := func(f automaticMultiFixture, want string) domain.AutomaticStockView {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			advance()
			v, e := db.AutomaticStockStatus(ctx, f.scope)
			if e != nil {
				t.Fatal(e)
			}
			if v.LastCycle != nil && v.LastCycle.State == want {
				return v
			}
			time.Sleep(50 * time.Millisecond)
		}
		v, _ := db.AutomaticStockStatus(ctx, f.scope)
		t.Fatalf("cycle not %s: %+v", want, v.LastCycle)
		return v
	}
	startReads(a, "")
	first := waitState(a, "WAITING_PUBLICATION")
	for i := 0; i < 8; i++ {
		worked, e := runner.RunNext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if worked {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	unknown := waitState(a, "BLOCKED")
	if unknown.LastCycle.Outcome != "PUBLICATION_UNKNOWN" {
		t.Fatalf("unknown not isolated: %+v", unknown.LastCycle)
	}
	if first.LastCycle.PublicationID == "" || writes.Load() != 1 {
		t.Fatal("first target was not sent once")
	}
	if _, err := db.RequestAutomaticCycle(ctx, a.scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	startReads(a, unknown.LastCycle.ID)
	second := waitState(a, "WAITING_PUBLICATION")
	for i := 0; i < 10; i++ {
		worked, e := runner.RunNext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if worked {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	done := waitState(a, "COMPLETE")
	if done.LastCycle.Outcome != "RECONCILED" || writes.Load() != 2 {
		t.Fatalf("unrelated variant did not reconcile: %+v writes=%d", done.LastCycle, writes.Load())
	}
	if len(done.Unresolved) != 1 || done.Unresolved[0].ProductID != "101" || done.Unresolved[0].State != "UNKNOWN" || done.Unresolved[0].OperationID != first.LastCycle.PublicationID {
		t.Fatalf("earlier unknown target disappeared after another publication: %+v", done.Unresolved)
	}
	op, err := db.StockPublication(ctx, a.scope, second.LastCycle.PublicationID)
	if err != nil || len(op.Targets) != 1 || op.Targets[0].ProductID != "102" || op.Targets[0].WarehouseID != "202" || op.Targets[0].ReadbackQuantity == nil || *op.Targets[0].ReadbackQuantity != "4" {
		t.Fatalf("wrong independent target: %+v %v", op, err)
	}
	for i := range a.market.Stocks {
		if a.market.Stocks[i].ProductID == "102" {
			a.market.Stocks[i].Present = 4
			a.market.Stocks[i].FreeStock = 4
		}
	}
	if _, err = db.RequestAutomaticCycle(ctx, a.scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	startReads(a, done.LastCycle.ID)
	blocked := waitState(a, "BLOCKED")
	if blocked.LastCycle.Outcome != "TARGETS_NEED_ATTENTION" || writes.Load() != 2 {
		t.Fatalf("unknown/shared pool bypassed or duplicate write: %+v", blocked.LastCycle)
	}
	b := automaticMultiScope(t, db, "two", false)
	startReads(b, "")
	bpub := waitState(b, "WAITING_PUBLICATION")
	for i := 0; i < 10; i++ {
		worked, e := runner.RunNext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if worked {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	bDone := waitState(b, "COMPLETE")
	if bDone.LastCycle.Outcome != "RECONCILED" || writes.Load() != 3 {
		t.Fatalf("independent account did not reconcile: %+v", bDone.LastCycle)
	}
	bop, err := db.StockPublication(ctx, b.scope, bpub.LastCycle.PublicationID)
	if err != nil || bop.OzonAccount == a.scope.OzonAccount || bop.Targets[0].ReadbackQuantity == nil || *bop.Targets[0].ReadbackQuantity != "5" {
		t.Fatalf("cross-account result: %+v %v", bop, err)
	}
	var unknownCount, blockedShared int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE product_id='101' AND state='UNKNOWN'),count(*) FILTER(WHERE product_id IN ('103','104')) FROM stock_publication_targets WHERE ozon_account=$1`, a.scope.OzonAccount).Scan(&unknownCount, &blockedShared); err != nil || unknownCount != 1 || blockedShared != 0 {
		t.Fatalf("unknown=%d shared-pool publications=%d err=%v", unknownCount, blockedShared, err)
	}
	// If a continuously changing first product is eligible again, the other
	// changed product must get its turn rather than being starved by ordering.
	c := automaticMultiScope(t, db, "three", true)
	startReads(c, "")
	cfirst := waitState(c, "WAITING_PUBLICATION")
	for i := 0; i < 10; i++ {
		worked, e := runner.RunNext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if worked {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	cfirstDone := waitState(c, "COMPLETE")
	cfirstOp, err := db.StockPublication(ctx, c.scope, cfirst.LastCycle.PublicationID)
	if err != nil || cfirstOp.Targets[0].ProductID != "101" {
		t.Fatalf("first fair target: %+v %v", cfirstOp, err)
	}
	mu.Lock()
	market[fmt.Sprintf("%s:%d:%d", c.scope.OzonAccount, 101, 201)] = 0
	mu.Unlock()
	if _, err = db.RequestAutomaticCycle(ctx, c.scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	startReads(c, cfirstDone.LastCycle.ID)
	csecond := waitState(c, "WAITING_PUBLICATION")
	for i := 0; i < 10; i++ {
		worked, e := runner.RunNext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if worked {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	csecondDone := waitState(c, "COMPLETE")
	secondOp, err := db.StockPublication(ctx, c.scope, csecond.LastCycle.PublicationID)
	if err != nil || csecondDone.LastCycle.Outcome != "RECONCILED" || secondOp.Targets[0].ProductID != "102" || writes.Load() != 5 {
		t.Fatalf("another changing target was starved: %+v writes=%d err=%v", secondOp, writes.Load(), err)
	}
	// Two due account pairs compete for the same scheduler. Concurrent workers
	// must advance both fairly while persisting only one cycle per account.
	d := automaticMultiScope(t, db, "four", false)
	e := automaticMultiScope(t, db, "five", false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, _, err := db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
					t.Errorf("concurrent scheduler: %v", err)
				}
			}()
		}
		wg.Wait()
		dv, de := db.AutomaticStockStatus(ctx, d.scope)
		ev, ee := db.AutomaticStockStatus(ctx, e.scope)
		if de != nil || ee != nil {
			t.Fatalf("scope status: %v %v", de, ee)
		}
		if dv.LastCycle != nil && ev.LastCycle != nil && dv.LastCycle.State == "READING" && ev.LastCycle.State == "READING" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	for _, scope := range []domain.StockSyncScope{d.scope, e.scope} {
		var cycles, imports int
		if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_auto_cycles WHERE tenant_id=$1 AND state IN ('STARTING','READING','WAITING_PUBLICATION')),(SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND principal_type='AUTOMATIC_POLICY')`, scope.Tenant).Scan(&cycles, &imports); err != nil || cycles != 1 || imports != 2 {
			t.Fatalf("unfair or duplicate scheduling for %s: cycles=%d imports=%d err=%v", scope.Tenant, cycles, imports, err)
		}
	}
}
