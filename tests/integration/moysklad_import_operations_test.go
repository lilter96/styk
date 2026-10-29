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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
)

func newMoyOperation(t *testing.T) importFixture {
	f := newImportFixture(t)
	if err := f.db.RegisterStockConnection(context.Background(), f.tenant, "moysklad", f.account, "synthetic operator", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Finish intentionally interrupted attempts so global legacy claim tests remain isolated.
		rows, err := f.db.Pool.Query(context.Background(), `SELECT id FROM ozon_import_operations WHERE tenant_id=$1 AND provider='moysklad' AND finished_at IS NULL`, f.tenant)
		if err != nil {
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			_ = f.db.CancelCatalogImport(context.Background(), f.tenant, "moysklad", f.account, id)
		}
	})
	return f
}
func submitMoy(t *testing.T, f importFixture, key string, l domain.ImportLimits) domain.ImportOperation {
	t.Helper()
	op, _, err := f.db.SubmitCatalogImport(context.Background(), f.tenant, "moysklad", f.account, key, "synthetic-session", l)
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func claimMoy(t *testing.T, f importFixture, lease time.Duration) domain.Task {
	t.Helper()
	task, err := f.db.ClaimForCatalogWorker(context.Background(), lease, "", f.account)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	return *task
}
func inspectMoy(t *testing.T, f importFixture, id string) domain.ImportOperation {
	t.Helper()
	op, err := f.db.GetCatalogOperation(context.Background(), f.tenant, "moysklad", f.account, id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func moyCatalogServer(t *testing.T, account string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-moy-token" {
			t.Error("unexpected external request")
			w.WriteHeader(405)
			return
		}
		var rows []map[string]any
		ref := func(kind, id string) map[string]any {
			return map[string]any{"meta": map[string]string{"href": "/entity/" + kind + "/" + id, "type": kind}}
		}
		switch r.URL.Path {
		case "/entity/product":
			rows = []map[string]any{{"id": "p1", "accountId": account, "article": "synthetic", "code": "code", "updated": "2026-01-01 12:00:00.000"}, {"id": "p2", "accountId": account}}
		case "/entity/variant":
			rows = []map[string]any{{"id": "v1", "accountId": account, "product": ref("product", "p1")}}
		case "/entity/store":
			rows = []map[string]any{{"id": "w1", "accountId": account}, {"id": "w2", "accountId": account}}
		case "/report/stock/all":
			rows = []map[string]any{{"meta": ref("product", "p1")["meta"], "stock": 10, "reserve": 2, "inTransit": 1, "quantity": 9}, {"meta": ref("variant", "v1")["meta"], "stock": 0, "reserve": 0, "inTransit": 0, "quantity": 0}}
		case "/report/stock/bystore":
			rows = []map[string]any{{"meta": ref("variant", "v1")["meta"], "stockByStore": []map[string]any{{"meta": ref("store", "w1")["meta"], "stock": 0, "reserve": 0, "inTransit": 0}, {"meta": ref("store", "w2")["meta"], "stock": 4, "reserve": 1, "inTransit": 2}}}}
		default:
			t.Error("non-catalog request")
			w.WriteHeader(405)
			return
		}
		offset := 0
		if r.URL.Query().Get("offset") == "1" {
			offset = 1
		}
		end := offset + 1
		if end > len(rows) {
			end = len(rows)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]int{"offset": offset, "size": len(rows), "limit": 1}, "rows": rows[offset:end]})
	}))
}
func moyClient(t *testing.T, s *httptest.Server) *moysklad.Client {
	t.Helper()
	c, err := moysklad.New(moysklad.Options{BaseURL: s.URL + "/", Token: "synthetic-moy-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 2, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func runMoy(t *testing.T, f importFixture, c *moysklad.Client, op domain.ImportOperation) {
	t.Helper()
	task := claimMoy(t, f, time.Second)
	if err := (ozonjob.Runner{Store: f.db, MoyskladAccount: f.account, MoyskladClient: c}).ProcessImport(context.Background(), task, time.Second); err != nil {
		t.Fatal(err)
	}
}
func TestMoyOperationCompleteValuesAndEligibleReuse(t *testing.T) {
	f := newMoyOperation(t)
	srv := moyCatalogServer(t, f.account)
	defer srv.Close()
	c := moyClient(t, srv)
	first := submitMoy(t, f, "first", domain.DefaultMoyskladImportLimits())
	runMoy(t, f, c, first)
	got := inspectMoy(t, f, first.ID)
	if got.State != "SUCCEEDED" || got.BatchID == nil || got.BatchCreated == nil || !*got.BatchCreated || got.Counts.Products != 2 || got.Counts.Variants != 1 || got.Counts.Warehouses != 2 || got.Counts.Observations != 2 || got.Counts.WarehouseObservations != 2 || got.Counts.Pages != 8 {
		t.Fatalf("unexpected result %+v", got)
	}
	var parent, sourceTime string
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT parent_id FROM source_entities WHERE import_id=$1 AND kind='variant' AND external_id='v1'`, *got.BatchID).Scan(&parent); err != nil || parent != "p1" {
		t.Fatal("variant identity lost", err)
	}
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT source_updated FROM source_entities WHERE import_id=$1 AND kind='product' AND external_id='p1'`, *got.BatchID).Scan(&sourceTime); err != nil || sourceTime != "2026-01-01 12:00:00.000" {
		t.Fatal("source timestamp lost", err)
	}
	var zero, nullable, missing int
	err := f.db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM source_warehouse_stock_observations WHERE import_id=$1 AND stock=0 AND reserve=0 AND in_transit=0),(SELECT count(*) FROM source_warehouse_stock_observations WHERE import_id=$1 AND quantity IS NULL),(SELECT count(*) FROM source_stock_observations WHERE import_id=$1 AND external_id='p2')`, *got.BatchID).Scan(&zero, &nullable, &missing)
	if err != nil || zero != 1 || nullable != 2 || missing != 0 {
		t.Fatal("stock semantics changed", err)
	}
	var exact, timestamps int
	err = f.db.Pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM source_stock_observations WHERE import_id=$1 AND kind='product' AND external_id='p1' AND stock=10 AND reserve=2 AND in_transit=1 AND quantity=9 AND free_stock IS NULL) +
 (SELECT count(*) FROM source_warehouse_stock_observations WHERE import_id=$1 AND kind='variant' AND external_id='v1' AND warehouse_id='w2' AND stock=4 AND reserve=1 AND in_transit=2 AND quantity IS NULL),
 (SELECT count(*) FROM source_warehouse_stock_observations o JOIN source_imports i ON i.id=o.import_id WHERE o.import_id=$1 AND o.observed_at=i.finished_at AND i.account_id=$2 AND i.source='moysklad')`, *got.BatchID, f.account).Scan(&exact, &timestamps)
	if err != nil || exact != 2 || timestamps != 2 {
		t.Fatal("reported values or observation timestamps changed", err)
	}
	second := submitMoy(t, f, "second", domain.DefaultMoyskladImportLimits())
	runMoy(t, f, c, second)
	reused := inspectMoy(t, f, second.ID)
	if reused.State != "SUCCEEDED" || *reused.BatchID != *got.BatchID || *reused.BatchCreated {
		t.Fatal("eligible identical batch not reused")
	}
	var n int
	_ = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE source='moysklad' AND account_id=$1`, f.account).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate batch")
	}
	for _, table := range []string{"stock_pools", "stock_decisions", "product_mappings", "warehouse_mappings"} {
		if err := f.db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", f.tenant).Scan(&n); err != nil || n != 0 {
			t.Fatal("unexpected side effects", table, err)
		}
	}
}
func TestMoySubmissionScopeNamespacesAndConcurrency(t *testing.T) {
	f := newMoyOperation(t)
	ctx := context.Background()
	l := domain.DefaultMoyskladImportLimits()
	for _, scope := range [][2]string{{"wrong", f.account}, {f.tenant, "wrong"}} {
		if _, _, err := f.db.SubmitCatalogImport(ctx, scope[0], "moysklad", scope[1], "invalid", "s", l); !errors.Is(err, postgres.ErrImportScope) {
			t.Fatal("scope bypass", err)
		}
	}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, _, err := f.db.SubmitCatalogImport(ctx, f.tenant, "moysklad", f.account, "same", "s", l)
			ids <- op.ID
			errs <- err
		}()
	}
	wg.Wait()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	id := <-ids
	if id != <-ids {
		t.Fatal("duplicate operation")
	}
	changed := l
	changed.Requests--
	if _, _, err := f.db.SubmitCatalogImport(ctx, f.tenant, "moysklad", f.account, "same", "s", changed); !errors.Is(err, postgres.ErrImportIdempotency) {
		t.Fatal("payload collision", err)
	}
	if _, _, err := f.db.SubmitCatalogImport(ctx, f.tenant, "moysklad", f.account, "another", "s", l); !errors.Is(err, postgres.ErrImportConflict) {
		t.Fatal("overlapping import", err)
	}
	// Same textual account can have a separate Ozon operation.
	oz := f.submit(t, "oz-separate", domain.DefaultOzonImportLimits())
	if err := f.db.CancelQueuedImport(ctx, f.tenant, f.account, oz.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.GetImportOperation(ctx, f.tenant, f.account, id); !errors.Is(err, postgres.ErrImportNotFound) {
		t.Fatal("provider namespace leak")
	}
	if _, _, err := f.db.ListCatalogOperations(ctx, "wrong", "moysklad", f.account, 10, 0); !errors.Is(err, postgres.ErrImportScope) {
		t.Fatal("tenant enumeration")
	}
	if err := f.db.CancelCatalogImport(ctx, f.tenant, "ozon", f.account, id); !errors.Is(err, postgres.ErrImportNotFound) {
		t.Fatal("wrong provider cancellation")
	}
	if err := f.db.CancelCatalogImport(ctx, f.tenant, "moysklad", f.account, id); err != nil {
		t.Fatal(err)
	}
	if inspectMoy(t, f, id).State != "CANCELLED" {
		t.Fatal("queued cancellation failed")
	}
}
func TestMoyIncompletePreservesPreviousBatch(t *testing.T) {
	for _, resource := range []string{"requests", "response_bytes", "products"} {
		t.Run(resource, func(t *testing.T) {
			f := newMoyOperation(t)
			s := moyCatalogServer(t, f.account)
			defer s.Close()
			c := moyClient(t, s)
			first := submitMoy(t, f, "first", domain.DefaultMoyskladImportLimits())
			runMoy(t, f, c, first)
			l := domain.DefaultMoyskladImportLimits()
			switch resource {
			case "requests":
				l.Requests = 1
			case "response_bytes":
				l.ResponseBytes = 8
			case "products":
				l.Products = 1
			}
			op := submitMoy(t, f, "failed", l)
			runMoy(t, f, c, op)
			got := inspectMoy(t, f, op.ID)
			if got.State != "FAILED" || got.BatchID != nil || got.Outcome == nil || *got.Outcome != "LIMIT_REACHED" || *got.LimitingResource != resource {
				t.Fatalf("incomplete committed %+v", got)
			}
			var n int
			if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE source='moysklad' AND account_id=$1`, f.account).Scan(&n); err != nil || n != 1 {
				t.Fatal("previous batch lost", err)
			}
		})
	}
}
func moyEmpty(f importFixture) (domain.InventoryImport, string) {
	now := time.Now().UTC()
	snap := domain.InventoryImport{Account: f.account, StartedAt: now, FinishedAt: now}
	fp, _ := application.InventoryFingerprint(&snap)
	return snap, fp
}
func TestMoyStaleClaimCannotPersistAfterReclaim(t *testing.T) {
	f := newMoyOperation(t)
	ctx := context.Background()
	op := submitMoy(t, f, "crash", domain.DefaultMoyskladImportLimits())
	old := claimMoy(t, f, time.Second)
	_, err := f.db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, fp := moyEmpty(f)
	if err = f.db.CommitClaimedMoyskladImport(ctx, old, snap, fp, domain.ImportCounts{}, time.Second); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatal("stale worker persisted", err)
	}
	if err = f.db.RenewImportClaim(ctx, old, time.Second); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatal("expired claim renewed")
	}
	fresh := claimMoy(t, f, time.Second)
	if err = f.db.CommitClaimedMoyskladImport(ctx, fresh, snap, fp, domain.ImportCounts{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = f.db.FailImport(ctx, old, "API_FAILURE", "", domain.ImportCounts{}); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatal("stale completion overwrote success")
	}
	got := inspectMoy(t, f, op.ID)
	if got.State != "SUCCEEDED" || got.Attempt != 2 {
		t.Fatal("reclaim failed")
	}
}
func TestMoyPersistenceRollbackTimeoutAndScope(t *testing.T) {
	for _, mode := range []string{"wrong-account", "rollback", "timeout", "locked-timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newMoyOperation(t)
			ctx := context.Background()
			op := submitMoy(t, f, "rollback", domain.DefaultMoyskladImportLimits())
			task := claimMoy(t, f, 5*time.Second)
			snap, fp := moyEmpty(f)
			timeout := time.Second
			persistCtx := ctx
			cancel := func() {}
			switch mode {
			case "wrong-account":
				snap.Account = "wrong"
			case "rollback":
				snap.Entities = []domain.ExternalEntity{{Kind: "product", ID: "duplicate"}, {Kind: "product", ID: "duplicate"}}
			case "timeout":
				timeout = time.Nanosecond
			case "locked-timeout":
				held, err := f.db.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Rollback(context.Background())
				if _, err = held.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "moysklad:"+f.account); err != nil {
					t.Fatal(err)
				}
				timeout = 20 * time.Millisecond
			case "cancel":
				persistCtx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := f.db.CommitClaimedMoyskladImport(persistCtx, task, snap, fp, domain.ImportCounts{}, timeout)
			cancel()
			if err == nil {
				t.Fatal("invalid persistence succeeded")
			}
			got := inspectMoy(t, f, op.ID)
			if got.BatchID != nil || got.State != "RUNNING" {
				t.Fatal("partial completion")
			}
			var n int
			_ = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE account_id=$1`, f.account).Scan(&n)
			if n != 0 {
				t.Fatal("partial batch")
			}
			if err = f.db.FailImport(ctx, task, "PERSISTENCE_FAILURE", "", domain.ImportCounts{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMoyHiddenHistoricalBatchNotReused(t *testing.T) {
	f := newMoyOperation(t)
	ctx := context.Background()
	snap, fp := moyEmpty(f)
	snap.StartedAt = time.Now().Add(-time.Hour)
	snap.FinishedAt = snap.StartedAt
	if _, err := f.db.SaveInventoryImport(ctx, snap, fp); err != nil {
		t.Fatal(err)
	}
	op := submitMoy(t, f, "new", domain.DefaultMoyskladImportLimits())
	task := claimMoy(t, f, time.Second)
	snap, _ = moyEmpty(f)
	if err := f.db.CommitClaimedMoyskladImport(ctx, task, snap, fp, domain.ImportCounts{}, time.Second); err != nil {
		t.Fatal(err)
	}
	got := inspectMoy(t, f, op.ID)
	if !*got.BatchCreated {
		t.Fatal("hidden historical batch reused")
	}
}
func TestMoyHeartbeatAndWorkerShutdown(t *testing.T) {
	f := newMoyOperation(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/entity/product" {
			time.Sleep(550 * time.Millisecond)
		}
		fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
	}))
	defer s.Close()
	op := submitMoy(t, f, "heartbeat", domain.DefaultMoyskladImportLimits())
	task := claimMoy(t, f, 300*time.Millisecond)
	if err := (ozonjob.Runner{Store: f.db, MoyskladAccount: f.account, MoyskladClient: moyClient(t, s)}).ProcessImport(context.Background(), task, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if inspectMoy(t, f, op.ID).State != "SUCCEEDED" {
		t.Fatal("lease renewal failed")
	}
	// A stopped worker leaves a recoverable, uncommitted attempt.
	op = submitMoy(t, f, "shutdown", domain.DefaultMoyskladImportLimits())
	task = claimMoy(t, f, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (ozonjob.Runner{Store: f.db, MoyskladAccount: f.account, MoyskladClient: moyClient(t, s)}).ProcessImport(ctx, task, time.Second)
	if err == nil {
		t.Fatal("worker ignored cancellation")
	}
	if inspectMoy(t, f, op.ID).BatchID != nil {
		t.Fatal("stopped worker committed")
	}
	_ = f.db.FailImport(context.Background(), task, "CANCELLED", "", domain.ImportCounts{})
}
func TestMoyOperationSafeProviderErrors(t *testing.T) {
	for _, mode := range []string{"auth", "invalid", "timeout", "wrong-config"} {
		t.Run(mode, func(t *testing.T) {
			f := newMoyOperation(t)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "auth":
					w.WriteHeader(401)
					fmt.Fprint(w, "secret payload")
				case "timeout":
					<-r.Context().Done()
				default:
					fmt.Fprint(w, `{}`)
				}
			}))
			defer s.Close()
			c := moyClient(t, s)
			if mode == "timeout" {
				c, _ = moysklad.New(moysklad.Options{BaseURL: s.URL + "/", Token: "synthetic-moy-token", Timeout: 10 * time.Millisecond, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			}
			op := submitMoy(t, f, "failed", domain.DefaultMoyskladImportLimits())
			task := claimMoy(t, f, time.Second)
			account := f.account
			if mode == "wrong-config" {
				account = "other"
			}
			if err := (ozonjob.Runner{Store: f.db, MoyskladAccount: account, MoyskladClient: c}).ProcessImport(context.Background(), task, time.Second); err != nil {
				t.Fatal(err)
			}
			got := inspectMoy(t, f, op.ID)
			want := "API_FAILURE"
			if mode == "invalid" {
				want = "INVALID_RESPONSE"
			}
			if mode == "wrong-config" {
				want = "CONFIGURATION_UNAVAILABLE"
			}
			if got.State != "FAILED" || got.Outcome == nil || *got.Outcome != want || got.BatchID != nil {
				t.Fatalf("unexpected result %+v", got)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "secret payload") || strings.Contains(string(raw), "synthetic-moy-token") {
				t.Fatal("credential or raw payload leak")
			}
		})
	}
}

func TestMoyExistingWorkerDispatchesAndReportsProgress(t *testing.T) {
	f := newMoyOperation(t)
	s := moyCatalogServer(t, f.account)
	defer s.Close()
	op := submitMoy(t, f, "dispatch", domain.DefaultMoyskladImportLimits())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	c := moyClient(t, s)
	go func() {
		done <- (application.Worker{Store: f.db, Handler: application.MockHandler{}, Imports: ozonjob.Runner{Store: f.db, MoyskladAccount: f.account, MoyskladClient: c}, MoyskladImportAccount: f.account, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 5 * time.Millisecond, Lease: time.Second, Retry: time.Millisecond}).Run(ctx)
	}()
	for {
		got := inspectMoy(t, f, op.ID)
		if got.State == "SUCCEEDED" {
			break
		}
		if got.State == "FAILED" || ctx.Err() != nil {
			cancel()
			<-done
			t.Fatalf("worker did not complete: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestMoyAPIRequiresConfiguredCredentialsAndExactScope(t *testing.T) {
	f := newMoyOperation(t)
	const origin = "http://127.0.0.1:5173"
	t.Setenv("MOYSKLAD_ACCOUNT_ID", f.account)
	t.Setenv("MOYSKLAD_TOKEN", "")
	h, err := console.Handler(f.db, strings.Repeat("s", 32), origin)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := call("/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil)
	cookie := login.Result().Cookies()[0]
	input := fmt.Sprintf(`{"tenant":%q,"moysklad_account":%q,"operation_type":"MOYSKLAD_CATALOG_IMPORT","idempotency_key":"missing-credentials","acknowledge":true}`, f.tenant, f.account)
	if w := call("/api/imports", input, cookie); w.Code != 403 || !strings.Contains(w.Body.String(), "ACCOUNT_NOT_CONFIGURED") {
		t.Fatal("missing credentials accepted")
	}
	// The same untrusted account text cannot select another configured provider.
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-only")
	h, err = console.Handler(f.db, strings.Repeat("s", 32), origin)
	if err != nil {
		t.Fatal(err)
	}
	login = call("/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil)
	cookie = login.Result().Cookies()[0]
	mixed := strings.TrimSuffix(input, "}") + `,"ozon_account":"untrusted"}`
	if w := call("/api/imports", mixed, cookie); w.Code != 400 {
		t.Fatal("mixed provider scope accepted")
	}
	fake := strings.TrimSuffix(input, "}") + `,"principal":"fake"}`
	if w := call("/api/imports", fake, cookie); w.Code != 400 {
		t.Fatal("browser principal accepted")
	}
	var n int
	_ = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1`, f.tenant).Scan(&n)
	if n != 0 {
		t.Fatal("rejected request persisted operation")
	}
}

func TestMoyConcurrentManualImportAndClaimedImportShareDeduplication(t *testing.T) {
	f := newMoyOperation(t)
	ctx := context.Background()
	op := submitMoy(t, f, "concurrent", domain.DefaultMoyskladImportLimits())
	task := claimMoy(t, f, 5*time.Second)
	snap, fp := moyEmpty(f)
	errorsCh := make(chan error, 2)
	go func() { _, err := f.db.SaveInventoryImport(ctx, snap, fp); errorsCh <- err }()
	go func() {
		errorsCh <- f.db.CommitClaimedMoyskladImport(ctx, task, snap, fp, domain.ImportCounts{}, time.Second)
	}()
	for i := 0; i < 2; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	got := inspectMoy(t, f, op.ID)
	if got.State != "SUCCEEDED" {
		t.Fatal("operation not committed")
	}
	var n int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE source='moysklad' AND account_id=$1`, f.account).Scan(&n); err != nil || n != 1 {
		t.Fatal("concurrent duplicate batch", err)
	}
}
func TestMoyLeaseLossDuringHTTPStopsStaleTraversal(t *testing.T) {
	f := newMoyOperation(t)
	started := make(chan struct{})
	var first sync.Once
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blocked := false
		first.Do(func() { blocked = true; close(started) })
		if blocked {
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
	}))
	defer s.Close()
	op := submitMoy(t, f, "in-flight", domain.DefaultMoyskladImportLimits())
	task := claimMoy(t, f, time.Second)
	c := moyClient(t, s)
	done := make(chan error, 1)
	go func() {
		done <- (ozonjob.Runner{Store: f.db, MoyskladAccount: f.account, MoyskladClient: c}).ProcessImport(context.Background(), task, time.Second)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	fresh := claimMoy(t, f, 3*time.Second)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale worker succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lost lease did not cancel HTTP")
	}
	if inspectMoy(t, f, op.ID).BatchID != nil {
		t.Fatal("stale traversal committed")
	}
	if err := (ozonjob.Runner{Store: f.db, MoyskladAccount: f.account, MoyskladClient: c}).ProcessImport(context.Background(), fresh, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := inspectMoy(t, f, op.ID); got.State != "SUCCEEDED" || got.Attempt != 2 {
		t.Fatal("recovery failed")
	}
}

func TestMoyDatabaseRejectsUnauthorizedBatchReferences(t *testing.T) {
	for _, mode := range []string{"other-provider", "other-account", "hidden-history"} {
		t.Run(mode, func(t *testing.T) {
			f := newMoyOperation(t)
			ctx := context.Background()
			snap, fp := moyEmpty(f)
			switch mode {
			case "other-provider":
				now := time.Now().UTC()
				if _, err := f.db.SaveOzonImport(ctx, domain.OzonImport{Account: f.account, StartedAt: now, FinishedAt: now}, "synthetic-ozon"); err != nil {
					t.Fatal(err)
				}
			case "other-account":
				snap.Account += "-other"
				if _, err := f.db.SaveInventoryImport(ctx, snap, fp); err != nil {
					t.Fatal(err)
				}
			case "hidden-history":
				snap.StartedAt = time.Now().Add(-time.Hour)
				snap.FinishedAt = snap.StartedAt
				if _, err := f.db.SaveInventoryImport(ctx, snap, fp); err != nil {
					t.Fatal(err)
				}
			}
			var batch int64
			provider := "moysklad"
			if mode == "other-provider" {
				provider = "ozon"
			}
			if err := f.db.Pool.QueryRow(ctx, `SELECT id FROM source_imports WHERE source=$1 AND account_id=$2 ORDER BY id DESC LIMIT 1`, provider, snap.Account).Scan(&batch); err != nil {
				t.Fatal(err)
			}
			op := submitMoy(t, f, "bad-batch", domain.DefaultMoyskladImportLimits())
			task := claimMoy(t, f, time.Second)
			tx, err := f.db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `UPDATE ozon_import_operations SET phase='COMPLETED',outcome='COMPLETE_TRAVERSAL',batch_id=$2,batch_created=false,finished_at=clock_timestamp() WHERE id=$1`, op.ID, batch)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='succeeded',lease_until=NULL,claim_token=NULL WHERE id=$1`, task.ID)
			}
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
			if err == nil {
				t.Fatal("unauthorized completed batch accepted")
			}
			_ = tx.Rollback(ctx)
			got := inspectMoy(t, f, op.ID)
			if got.State != "RUNNING" || got.BatchID != nil {
				t.Fatal("partial forged success")
			}
			if err = f.db.FailImport(ctx, task, "PERSISTENCE_FAILURE", "", domain.ImportCounts{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
