//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

// The fixed real account scope is exercised only against a loopback provider.
// No real credentials or hosts occur in this test.
func TestControlledAutomaticEnrolledTargetsShareTraversal(t *testing.T) {
	db, _ := controlledFixture(t)
	scope := testpublication.Scope()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	const second = "2764939447"
	const secondOffer = "ATEST-0010987"
	const secondItem = "ea33e770-d389-11f1-0a80-0d480029c772"
	now := time.Now().UTC().Add(-time.Second)
	zero, four, ten, three := "0", "4", "10", "3"
	fbs, archived := false, false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now.Add(time.Millisecond), Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct}, {Kind: "product", ID: secondItem}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &four, Reserve: &zero, InTransit: &zero}, {Kind: "product", ExternalID: secondItem, WarehouseID: testpublication.SourceWarehouse, Stock: &ten, Reserve: &three, InTransit: &zero}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now.Add(time.Millisecond), Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}, {ProductID: second, OfferID: secondOffer, SKU: "2889575995", Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, Status: "created", IsRFBS: &fbs}}, Stocks: []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, WarehouseID: testpublication.OzonWarehouse, Present: 0, Reserved: 0, FreeStock: 0}, {ProductID: second, OfferID: secondOffer, SKU: "2889575995", WarehouseID: testpublication.OzonWarehouse, Present: 0, Reserved: 0, FreeStock: 0}}}
	if _, err := db.SaveInventoryImport(ctx, source, "controlled-multi-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "controlled-multi-market"); err != nil {
		t.Fatal(err)
	}
	review, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, ProductID: second, TargetKind: "product", TargetID: secondItem, Evidence: domain.MappingOperatorAttested, SessionID: "synthetic", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: "synthetic", IdempotencyKey: "synthetic-multi-reserve", Acknowledged: true, Explanation: "Synthetic reserved product mapping"}); err != nil {
		t.Fatal(err)
	}
	inputs := []domain.AutomaticStockTargetRequest{}
	for _, id := range []string{testpublication.OzonProduct, second} {
		inputs = append(inputs, domain.AutomaticStockTargetRequest{ProductID: id, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7})
	}
	configured, err := db.ConfigureAutomaticStock(ctx, domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, Targets: inputs, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "controlled-multi-policy-1"}, time.Now())
	if err != nil || len(configured.Policy.Targets) != 2 || len(configured.Candidates) != 2 {
		t.Fatalf("multi-target policy: %+v %v", configured, err)
	}
	bad := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, Targets: []domain.AutomaticStockTargetRequest{{ProductID: "999", WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7}}, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "controlled-multi-policy-bad"}
	if _, err = db.ConfigureAutomaticStock(ctx, bad, time.Now()); err == nil {
		t.Fatal("unmapped product was admitted")
	}
	bad.Targets = inputs
	bad.Targets[0].Maximum = 11
	bad.IdempotencyKey = "controlled-multi-policy-max"
	if _, err = db.ConfigureAutomaticStock(ctx, bad, time.Now()); err == nil {
		t.Fatal("quantity above the controlled test ceiling was admitted")
	}
	var mu sync.Mutex
	stock := map[int64]int64{}
	writes := map[int64]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/products/stocks":
			var body struct {
				Stocks []struct {
					ProductID   int64 `json:"product_id"`
					WarehouseID int64 `json:"warehouse_id"`
					Stock       int64 `json:"stock"`
				} `json:"stocks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Stocks) != 1 {
				t.Error("unexpected publication batch")
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			v := body.Stocks[0]
			mu.Lock()
			stock[v.ProductID] = v.Stock
			writes[v.ProductID]++
			mu.Unlock()
			fmt.Fprintf(w, `{"result":[{"product_id":%d,"warehouse_id":%d,"updated":true,"errors":[]}]}`, v.ProductID, v.WarehouseID)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			var body struct {
				OfferID []string `json:"offer_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := int64(2764544165)
			if len(body.OfferID) != 1 {
				http.Error(w, "invalid", 400)
				return
			}
			if body.OfferID[0] == secondOffer {
				id = 2764939447
			}
			mu.Lock()
			n := stock[id]
			mu.Unlock()
			fmt.Fprintf(w, `{"products":[{"product_id":%d,"warehouse_id":1020001323428000,"offer_id":%q,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, id, body.OfferID[0], n, n)
		default:
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	client, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: "synthetic", APIKey: "synthetic", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	runner := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
	for i := 0; i < 20; i++ {
		if _, _, err = db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		view, e := db.AutomaticStockStatus(ctx, scope)
		if e != nil {
			t.Fatal(e)
		}
		if view.LastCycle != nil && view.LastCycle.State == "READING" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		task, e := db.ClaimForCatalogWorker(ctx, time.Minute, scope.OzonAccount, scope.MoyskladAccount)
		if e != nil || task == nil {
			t.Fatalf("claim import: %+v %v", task, e)
		}
		timestamp := time.Now().UTC()
		counts := domain.ImportCounts{Attempts: 1, Requests: 1, CompletedRequests: 1}
		if task.Type == domain.MoyskladCatalogTask {
			source.StartedAt, source.FinishedAt = timestamp, timestamp
			fingerprint, e := application.InventoryFingerprint(&source)
			if e != nil {
				t.Fatal(e)
			}
			if e = db.CommitClaimedMoyskladImport(ctx, *task, source, fingerprint, counts, 5*time.Second); e != nil {
				t.Fatal(e)
			}
		} else {
			market.StartedAt, market.FinishedAt = timestamp, timestamp
			fingerprint, e := application.OzonFingerprint(&market)
			if e != nil {
				t.Fatal(e)
			}
			if e = db.CommitClaimedOzonImport(ctx, *task, market, fingerprint, counts, 5*time.Second); e != nil {
				t.Fatal(e)
			}
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	var final domain.AutomaticStockView
	for time.Now().Before(deadline) {
		if _, _, err = db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err = runner.RunNext(ctx); err != nil {
			t.Fatal(err)
		}
		final, err = db.AutomaticStockStatus(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if final.LastCycle != nil && (final.LastCycle.State == "COMPLETE" || final.LastCycle.State == "BLOCKED") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final.LastCycle == nil || final.LastCycle.Outcome != "RECONCILED" {
		t.Fatalf("cycle: %+v", final.LastCycle)
	}
	mu.Lock()
	defer mu.Unlock()
	if stock[2764544165] != 4 || stock[2764939447] != 7 || writes[2764544165] != 1 || writes[2764939447] != 1 {
		t.Fatalf("stock=%v writes=%v", stock, writes)
	}
	var imports, publications, stored int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND principal_type='AUTOMATIC_POLICY'),(SELECT count(*) FROM stock_auto_cycle_publications WHERE cycle_id=$2),(SELECT count(*) FROM stock_auto_cycle_publications cp JOIN stock_publication_targets t ON t.operation_id=cp.publication_id WHERE cp.cycle_id=$2 AND cp.outcome='RECONCILED' AND t.state='RECONCILED' AND t.provider_updated=true AND cardinality(t.provider_error_codes)=0)`, scope.Tenant, final.LastCycle.ID).Scan(&imports, &publications, &stored); err != nil || imports != 2 || publications != 2 || stored != 2 {
		t.Fatalf("imports=%d publications=%d stored=%d error=%v", imports, publications, stored, err)
	}
	if final.LastCycle.Metrics["external_writes"] != float64(2) || final.LastCycle.Metrics["changed_targets"] != float64(2) || final.LastCycle.Metrics["reconciled_operations"] != float64(2) {
		t.Fatalf("multi-target metrics: %+v", final.LastCycle.Metrics)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE stock_auto_cycle_publications SET product_id='forged' WHERE cycle_id=$1`, final.LastCycle.ID); err == nil {
		t.Fatal("cycle publication association was mutable")
	}
	down := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
	if output, e := down.CombinedOutput(); e == nil || !strings.Contains(string(output), "refusing to discard multi-target cycle publication history") {
		t.Fatal("migration permitted loss of multi-target publication history")
	}
}
