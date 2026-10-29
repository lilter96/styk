//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
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

func TestAutomaticStockCyclesUseOutboxAndDeduplicate(t *testing.T) {
	db, _ := controlledFixture(t)
	scope := testpublication.Scope()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	var source, marketplace, writes atomic.Int32
	source.Store(7)
	marketplace.Store(7)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			fmt.Fprintf(w, `{"products":[{"product_id":2764544165,"offer_id":"ATEST-897677","sku":2889277907,"warehouse_id":1020001323428000,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, marketplace.Load(), marketplace.Load())
		case "/v2/products/stocks":
			var in struct {
				Stocks []struct {
					ProductID   int64 `json:"product_id"`
					WarehouseID int64 `json:"warehouse_id"`
					Stock       int32 `json:"stock"`
				} `json:"stocks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Stocks) != 1 || in.Stocks[0].ProductID != 2764544165 || in.Stocks[0].WarehouseID != 1020001323428000 {
				t.Errorf("wrong write target: %+v %v", in, err)
				http.Error(w, "bad target", 400)
				return
			}
			writes.Add(1)
			marketplace.Store(in.Stocks[0].Stock)
			fmt.Fprint(w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
		case "/entity/product":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Test","article":"synthetic"}]}`, testpublication.SourceProduct)
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`)
		case "/entity/store":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Test"}]}`, testpublication.SourceWarehouse)
		case "/report/stock/all":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stock":%d,"reserve":0,"inTransit":0,"quantity":%d}]}`, testpublication.SourceProduct, source.Load(), source.Load())
		case "/report/stock/bystore":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":%d,"reserve":0,"inTransit":0}]}]}`, testpublication.SourceProduct, testpublication.SourceWarehouse, source.Load())
		default:
			t.Errorf("unexpected provider path: %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	oz, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: scope.OzonAccount, APIKey: "synthetic", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	mo, err := moysklad.New(moysklad.Options{BaseURL: server.URL, Token: "synthetic", Timeout: 2 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	pub := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return oz, nil }}
	imports := ozonjob.Runner{Store: db, Account: scope.OzonAccount, Client: oz, MoyskladAccount: scope.MoyskladAccount, MoyskladClient: mo}
	worker := application.Worker{Store: db, Handler: application.MockHandler{}, Imports: imports, Publications: pub, Automatic: stocksync.AutomaticRunner{Store: db, Publisher: pub}, ImportAccount: scope.OzonAccount, MoyskladImportAccount: scope.MoyskladAccount, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 50 * time.Millisecond, Lease: time.Minute, Retry: time.Second}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	req := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7, Acknowledge: true, SessionID: "synthetic-operator", IdempotencyKey: "automatic-enable-0001"}
	bad := req
	bad.OzonAccount = "different-account"
	if _, err = db.ConfigureAutomaticStock(ctx, bad, time.Now()); err == nil {
		t.Fatal("wrong account accepted")
	}
	bad = req
	bad.Acknowledge = false
	if _, err = db.ConfigureAutomaticStock(ctx, bad, time.Now()); err == nil {
		t.Fatal("missing acknowledgement accepted")
	}
	view, err := db.ConfigureAutomaticStock(ctx, req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Policy.Enabled || view.Policy.EvidenceClass != "OPERATOR_ATTESTED" {
		t.Fatalf("wrong policy: %+v", view.Policy)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, _ = db.AdvanceAutomaticStock(ctx, time.Now()) }()
	}
	wg.Wait()
	waitCycle := func(outcome string, previous string) domain.AutomaticStockView {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		for time.Now().Before(deadline) {
			v, e := db.AutomaticStockStatus(ctx, scope)
			if e != nil {
				t.Fatal(e)
			}
			if v.LastCycle != nil && v.LastCycle.ID != previous && v.LastCycle.Outcome == outcome {
				return v
			}
			if v.LastCycle != nil && v.LastCycle.State == "BLOCKED" {
				t.Fatalf("cycle blocked: %+v", v.LastCycle)
			}
			time.Sleep(100 * time.Millisecond)
		}
		v, _ := db.AutomaticStockStatus(ctx, scope)
		t.Fatalf("cycle %s did not finish: %+v", outcome, v.LastCycle)
		return v
	}
	first := waitCycle("NO_CHANGE", "")
	if writes.Load() != 0 {
		t.Fatal("no-op wrote marketplace stock")
	}
	if first.LastCycle.Metrics["moysklad_requests"] != float64(5) || first.LastCycle.Metrics["ozon_requests"] != float64(4) || first.LastCycle.Metrics["external_writes"] != float64(0) {
		t.Fatalf("incorrect no-op provider metrics: %+v", first.LastCycle.Metrics)
	}
	source.Store(5)
	if _, err = db.RequestAutomaticCycle(ctx, scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	second := waitCycle("RECONCILED", first.LastCycle.ID)
	if writes.Load() != 1 || marketplace.Load() != 5 || second.LastCycle.PublicationID == "" {
		t.Fatalf("write=%d stock=%d cycle=%+v", writes.Load(), marketplace.Load(), second.LastCycle)
	}
	if second.LastCycle.Metrics["external_writes"] != float64(1) || second.LastCycle.Metrics["reconciled_operations"] != float64(1) || second.LastCycle.Metrics["readback_requests"] != float64(1) {
		t.Fatalf("incorrect publication metrics: %+v", second.LastCycle.Metrics)
	}
	if _, err = db.RequestAutomaticCycle(ctx, scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	third := waitCycle("NO_CHANGE", second.LastCycle.ID)
	if writes.Load() != 1 {
		t.Fatal("unchanged cycle repeated the write")
	}
	var originalCreated time.Time
	if err = db.Pool.QueryRow(ctx, `SELECT (snapshot->>'created_at')::timestamptz FROM stock_auto_config_audits WHERE revision=1`).Scan(&originalCreated); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE stock_auto_config_audits SET snapshot='{}'::jsonb WHERE revision=1`); err == nil {
		t.Fatal("policy audit was mutable")
	}
	var policyTime time.Time
	if err = db.Pool.QueryRow(ctx, `SELECT (snapshot->'attestations'->0->'proofs'->0->>'as_of')::timestamptz FROM stock_sync_reviews WHERE id=(SELECT review_id FROM stock_sync_settings WHERE tenant_id=$1)`, scope.Tenant).Scan(&policyTime); err != nil || !policyTime.Equal(originalCreated) {
		t.Fatalf("attestation time changed: %v %v %v", policyTime, originalCreated, err)
	}
	disable := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: false, Acknowledge: true, SessionID: "synthetic-operator", IdempotencyKey: "automatic-disable-0001"}
	if _, err = db.ConfigureAutomaticStock(ctx, disable, time.Now()); err != nil {
		t.Fatal(err)
	}
	source.Store(3)
	time.Sleep(1200 * time.Millisecond)
	stopped, err := db.AutomaticStockStatus(ctx, scope)
	if err != nil || stopped.Policy.Enabled || writes.Load() != 1 || stopped.LastCycle.ID != third.LastCycle.ID {
		t.Fatalf("pause failed: %+v %v", stopped, err)
	}
	var active int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_auto_cycles WHERE state IN ('STARTING','READING','WAITING_PUBLICATION')`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("active cycle count %d: %v", active, err)
	}
	req.IdempotencyKey = "automatic-resume-0001"
	if _, err = db.ConfigureAutomaticStock(ctx, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	resumed := waitCycle("RECONCILED", third.LastCycle.ID)
	if writes.Load() != 2 || marketplace.Load() != 3 || resumed.LastCycle.PublicationID == second.LastCycle.PublicationID {
		t.Fatalf("resume failed: writes=%d market=%d cycle=%+v", writes.Load(), marketplace.Load(), resumed.LastCycle)
	}
}

func TestAutomaticPolicyCannotBypassUnknownFenceOrTenant(t *testing.T) {
	db, review := controlledFixture(t)
	ctx := context.Background()
	old := controlledEnable(t, db, review)
	claim, err := db.NextControlledStockPublication(ctx, time.Now().UTC())
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = db.FinishStockTarget(ctx, review.StockSyncScope, claim.ID, claim.Targets[0].ID, "UNKNOWN", "REQUEST_OUTCOME_UNKNOWN", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	req := domain.AutomaticStockRequest{StockSyncScope: review.StockSyncScope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7, Acknowledge: true, SessionID: "synthetic-session", IdempotencyKey: "auto-policy-unknown-01"}
	if _, err = db.ConfigureAutomaticStock(ctx, req, time.Now()); !errors.Is(err, domain.ErrStockSyncBlocked) {
		t.Fatalf("unknown fence bypassed: %v", err)
	}
	for _, mutate := range []func(*domain.AutomaticStockRequest){func(r *domain.AutomaticStockRequest) { r.Tenant = "another-tenant" }, func(r *domain.AutomaticStockRequest) { r.OzonAccount = "another-account" }, func(r *domain.AutomaticStockRequest) { r.Acknowledge = false }} {
		bad := req
		mutate(&bad)
		if _, err = db.ConfigureAutomaticStock(ctx, bad, time.Now()); err == nil {
			t.Fatal("unapproved scope or request enabled")
		}
	}
	var state string
	var attempts int
	if err = db.Pool.QueryRow(ctx, `SELECT state,attempts FROM outbox_tasks WHERE publication_operation_id=$1`, old.LastOperation.ID).Scan(&state, &attempts); err != nil || state != "failed" || attempts != 1 {
		t.Fatalf("unknown replayed: %s/%d %v", state, attempts, err)
	}
	if op, err := db.NextControlledStockPublication(ctx, time.Now().Add(time.Minute)); err != nil || op != nil {
		t.Fatalf("unknown automatically retried: %+v %v", op, err)
	}
}

func TestAutomaticReadFailureClassificationAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		outcome string
		retry   bool
	}{
		{"authorization", 403, "AUTHORIZATION_REQUIRED", false},
		{"rate_limit", 429, "RATE_LIMITED", false},
		{"rate_limit_before_attempt_budget", -1, "RATE_LIMITED", false},
		{"unavailable", 500, "RETRYABLE_READ_FAILURE", true},
		{"transport", 0, "RETRYABLE_READ_FAILURE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := controlledFixture(t)
			ctx := context.Background()
			scope := testpublication.Scope()
			now := time.Now().UTC()
			req := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "automatic-read-failure-01"}
			if _, err := db.ConfigureAutomaticStock(ctx, req, now); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.AdvanceAutomaticStock(ctx, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			view, err := db.AutomaticStockStatus(ctx, scope)
			if err != nil || view.LastCycle == nil {
				t.Fatalf("cycle: %+v %v", view, err)
			}
			id := view.LastCycle.ID
			// Simulate a crash after import submission but before the cycle linked
			// its ID. Resumption must recover the same idempotent read operation.
			op, _, err := db.SubmitScheduledCatalogImport(ctx, scope.Tenant, "moysklad", scope.MoyskladAccount, "automatic:"+id+":moysklad", "automatic-policy:1", domain.DefaultMoyskladImportLimits())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				if _, _, err = db.AdvanceAutomaticStock(ctx, now.Add(time.Duration(i+2)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			view, err = db.AutomaticStockStatus(ctx, scope)
			if err != nil || view.LastCycle.MoyskladImportID != op.ID || view.LastCycle.State != "READING" {
				t.Fatalf("restart did not link import: %+v %v", view.LastCycle, err)
			}
			task, err := db.ClaimForCatalogWorker(ctx, time.Minute, scope.OzonAccount, scope.MoyskladAccount)
			if err != nil || task == nil || task.ImportOperationID != op.ID {
				t.Fatalf("claim: %+v %v", task, err)
			}
			counts := domain.ImportCounts{Attempts: 1, HTTPStatus: tc.status}
			if tc.status == 429 || tc.status == -1 {
				counts.RateLimitResponses = 1
			}
			if tc.status == -1 {
				counts.HTTPStatus = 0
			}
			if err = db.FailImport(ctx, *task, "API_FAILURE", "", counts); err != nil {
				t.Fatal(err)
			}
			other, e := db.ClaimForCatalogWorker(ctx, time.Minute, scope.OzonAccount, scope.MoyskladAccount)
			if e != nil || other == nil {
				t.Fatalf("claim second import: %+v %v", other, e)
			}
			if e = db.FailImport(ctx, *other, "API_FAILURE", "", domain.ImportCounts{Attempts: 1, HTTPStatus: 500}); e != nil {
				t.Fatal(e)
			}
			if _, _, err = db.AdvanceAutomaticStock(ctx, now.Add(10*time.Second)); err != nil {
				t.Fatal(err)
			}
			view, err = db.AutomaticStockStatus(ctx, scope)
			if err != nil || view.LastCycle.Outcome != tc.outcome || (view.NextRunAt != nil) != tc.retry {
				t.Fatalf("failure classification: %+v %v", view, err)
			}
			if tc.retry && view.NextRunAt.Before(now.Add(65*time.Second)) {
				t.Fatalf("retry backoff was too short: %v", view.NextRunAt)
			}
			if !tc.retry {
				if _, worked, err := db.AdvanceAutomaticStock(ctx, now.Add(time.Hour)); err != nil || worked {
					t.Fatalf("forbidden automatic retry: %v %v", worked, err)
				}
			}
			if tc.name == "authorization" {
				down := func() ([]byte, error) {
					return exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down").CombinedOutput()
				}
				if _, e := down(); e != nil {
					t.Fatal("could not roll back enrolled recovery migration")
				}
				if _, e := down(); e != nil {
					t.Fatal("could not roll back empty cycle-target migration")
				}
				if _, e := down(); e != nil {
					t.Fatal("could not roll back empty provider-result migration")
				}
				out, e := down()
				if e == nil || !strings.Contains(string(out), "refusing to discard automatic synchronization audit") {
					t.Fatal("migration did not protect automatic policy history")
				}
			}
		})
	}
}
