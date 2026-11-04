//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

type stockAdmissionHTTP struct {
	scope    domain.StockSyncScope
	quantity atomic.Int64
	writes   atomic.Int32
	reads    atomic.Int32
	limited  atomic.Bool
}

func (p *stockAdmissionHTTP) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Header.Get("Client-Id") != p.scope.OzonAccount || r.Header.Get("Api-Key") != "synthetic-admission-only" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/v2/products/stocks" {
		var body struct {
			Stocks []struct {
				Stock       int64 `json:"stock"`
				ProductID   int64 `json:"product_id"`
				WarehouseID int64 `json:"warehouse_id"`
			} `json:"stocks"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || len(body.Stocks) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Stocks[0].ProductID != 2764544165 || body.Stocks[0].WarehouseID != 1020001323428000 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.writes.Add(1)
		p.quantity.Store(body.Stocks[0].Stock)
		fixtureText(t, w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
		return
	}
	if r.URL.Path != "/v2/product/info/stocks-by-warehouse/fbs" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	p.reads.Add(1)
	if p.limited.Load() {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	fixturePrintf(t, w, `{"products":[{"product_id":2764544165,"warehouse_id":1020001323428000,"offer_id":"ATEST-897677","free_stock":%d,"reserved":0,"present":%d}],"has_next":false,"cursor":""}`, p.quantity.Load(), p.quantity.Load())
}

func stockAdmissionClient(t *testing.T, db *postgres.Store, scope domain.StockSyncScope, provider *stockAdmissionHTTP) *ozon.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { provider.serve(t, w, r) }))
	t.Cleanup(server.Close)
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,'ozon',$2,'Synthetic admission fixture',1,$3,clock_timestamp(),'synthetic-admission')`, scope.Tenant, scope.OzonAccount, make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	client, err := ozon.New(ozon.Options{AllowSyntheticStockWrites: true, BaseURL: server.URL, ClientID: scope.OzonAccount, APIKey: "synthetic-admission-only", Timeout: 250 * time.Millisecond, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Millisecond, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Coordinate(gate, providercall.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	return client
}

func stockAdmissionRunner(db *postgres.Store, resolve func(context.Context, string, string) (*ozon.Client, error)) stocksync.Runner {
	return stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: resolve}
}

func TestStockAdmissionWaitTimeoutBeforeHTTPDoesNotBecomeUnknown(t *testing.T) {
	db, review := controlledFixture(t)
	provider := &stockAdmissionHTTP{scope: review.StockSyncScope}
	provider.limited.Store(true)
	client := stockAdmissionClient(t, db, review.StockSyncScope, provider)
	review = stockAdmissionReview(t, db, review)
	view := controlledEnable(t, db, review)
	runner := stockAdmissionRunner(db, func(ctx context.Context, _, _ string) (*ozon.Client, error) {
		if _, err := client.ReadStockQuantity(ctx, testpublication.OzonProduct, testpublication.OzonWarehouse, testpublication.OzonOffer); err == nil {
			t.Fatal("synthetic shared429 precondition missing")
		}
		return client, nil
	})
	if worked, err := runner.RunNext(context.Background()); err != nil || !worked {
		t.Fatal("normal claimed publication worker failed", err)
	}
	op, err := db.StockPublication(context.Background(), review.StockSyncScope, view.LastOperation.ID)
	if err != nil || len(op.Targets) != 1 {
		t.Fatal("publication evidence missing", err)
	}
	if provider.writes.Load() != 0 || provider.reads.Load() != 1 {
		t.Fatal("cooldown wait unexpectedly dispatched a stock write")
	}
	if op.Targets[0].State != "BLOCKED" || op.State == "UNKNOWN" || op.Targets[0].SafeError != "PROVIDER_REQUEST_NOT_SENT" {
		t.Fatalf("proven zero-dispatch wait became ambiguous: operation=%s target=%s code=%s", op.State, op.Targets[0].State, op.Targets[0].SafeError)
	}
	if worked, err := runner.RunNext(context.Background()); err != nil || worked || provider.writes.Load() != 0 {
		t.Fatal("terminal unsent intent was automatically replayed", err)
	}
}

func TestStockSharedTargetCooldownDefersClaimUntilEligible(t *testing.T) {
	db, review := controlledFixture(t)
	provider := &stockAdmissionHTTP{scope: review.StockSyncScope}
	client := stockAdmissionClient(t, db, review.StockSyncScope, provider)
	review = stockAdmissionReview(t, db, review)
	first := controlledEnable(t, db, review)
	runner := stockAdmissionRunner(db, func(context.Context, string, string) (*ozon.Client, error) { return client, nil })
	if worked, err := runner.RunNext(context.Background()); err != nil || !worked {
		t.Fatal(err)
	}
	op, err := db.StockPublication(context.Background(), review.StockSyncScope, first.LastOperation.ID)
	if err != nil || op.State != "RECONCILED" || provider.writes.Load() != 1 {
		t.Fatal("normal first publication did not establish target cooldown", err)
	}
	next := stockAdmissionNextDecision(t, db, review)
	view, err := db.MutateStockSync(context.Background(), review.StockSyncScope, "enable", next.ID, "admission-next-intent-001", time.Now())
	if err != nil || view.LastOperation == nil || view.LastOperation.ID == first.LastOperation.ID {
		t.Fatal("new authorized source decision missing", err)
	}
	claim, err := db.NextControlledStockPublication(context.Background(), time.Now().Add(31*time.Second))
	if err != nil || claim != nil {
		t.Fatal("caller clock bypassed shared DB-clock target cooldown after the legacy30s interval", err)
	}
	op, err = db.StockPublication(context.Background(), review.StockSyncScope, view.LastOperation.ID)
	if err != nil || op.State != "QUEUED" || provider.writes.Load() != 1 {
		t.Fatal("cooldown target was dispatched or misclassified instead of deferred", err)
	}
}

func stockAdmissionNextDecision(t *testing.T, db *postgres.Store, previous domain.StockSyncReview) domain.StockSyncReview {
	t.Helper()
	now, quantity, zero := time.Now().UTC(), "6", "0"
	scope := previous.StockSyncScope
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct, Article: "synthetic"}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &quantity, Reserve: &zero, InTransit: &zero}}}
	if _, err := db.SaveInventoryImport(context.Background(), source, "admission-source-six"); err != nil {
		t.Fatal(err)
	}
	review, err := db.PreviewStockSync(context.Background(), domain.StockSyncPreviewRequest{StockSyncScope: scope, Attestations: previous.Attestations}, time.Now())
	if err != nil || !review.Ready || len(review.Targets) != 1 || review.Targets[0].Quantity == nil || *review.Targets[0].Quantity != "6" {
		t.Fatal("fresh source6 did not yield an authoritative numeric decision", err)
	}
	return review
}

func stockAdmissionReview(t *testing.T, db *postgres.Store, previous domain.StockSyncReview) domain.StockSyncReview {
	t.Helper()
	review, err := db.PreviewStockSync(context.Background(), domain.StockSyncPreviewRequest{StockSyncScope: previous.StockSyncScope, Attestations: previous.Attestations}, time.Now())
	if err != nil || !review.Ready {
		t.Fatal("review after credential enrollment was not ready", err)
	}
	return review
}
