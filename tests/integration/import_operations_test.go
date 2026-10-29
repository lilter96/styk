//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
)

type importFixture struct {
	db              *postgres.Store
	tenant, account string
}

func TestImportReconcilesLostDatabaseCommitResponse(t *testing.T) {
	for _, provider := range []string{"ozon", "moysklad"} {
		t.Run(provider, func(t *testing.T) {
			reconcileLostImportCommit(t, provider)
		})
	}
}
func reconcileLostImportCommit(t *testing.T, provider string) {
	f := newImportFixture(t)
	if provider == "moysklad" {
		if err := f.db.RegisterStockConnection(context.Background(), f.tenant, provider, f.account, "synthetic operator", false); err != nil {
			t.Fatal(err)
		}
	}
	var op domain.ImportOperation
	if provider == "moysklad" {
		op = submitMoy(t, f, "lost-commit", domain.DefaultMoyskladImportLimits())
	} else {
		op = f.submit(t, "lost-commit", domain.DefaultOzonImportLimits())
	}
	var task domain.Task
	if provider == "moysklad" {
		task = claimMoy(t, f, time.Second)
	} else {
		task = f.claim(t)
	}
	dbURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	target := dbURL.Host
	if dbURL.Port() == "" {
		target = net.JoinHostPort(dbURL.Hostname(), "5432")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(t, listener)
	dropped := make(chan struct{}, 1)
	go func() {
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				upstream, e := net.Dial("tcp", target)
				if e != nil {
					closeFixture(t, client)
					return
				}
				defer closeFixture(t, client)
				defer closeFixture(t, upstream)
				go func() { _, _ = io.Copy(upstream, client) }()
				for {
					header := make([]byte, 5)
					if _, e = io.ReadFull(upstream, header); e != nil {
						return
					}
					length := binary.BigEndian.Uint32(header[1:])
					if length < 4 || length > 16<<20 {
						return
					}
					payload := make([]byte, length-4)
					if _, e = io.ReadFull(upstream, payload); e != nil {
						return
					}
					// PostgreSQL has already committed when it emits CommandComplete.
					// Drop that message to simulate a lost commit response.
					if header[0] == 'C' && string(payload) == "COMMIT\x00" {
						select {
						case dropped <- struct{}{}:
						default:
						}
						return
					}
					if _, e = client.Write(header); e != nil {
						return
					}
					if _, e = client.Write(payload); e != nil {
						return
					}
				}
			}()
		}
	}()
	proxiedURL := *dbURL
	proxiedURL.Host = listener.Addr().String()
	query := proxiedURL.Query()
	query.Set("sslmode", "disable")
	proxiedURL.RawQuery = query.Encode()
	proxyDB, err := postgres.Open(context.Background(), proxiedURL.String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer proxyDB.Pool.Close()
	now := time.Now().UTC()
	snap := domain.OzonImport{Account: f.account, StartedAt: now, FinishedAt: now}
	fingerprint, err := application.OzonFingerprint(&snap)
	if err != nil {
		t.Fatal(err)
	}
	if provider == "moysklad" {
		ms, fp := moyEmpty(f)
		err = proxyDB.CommitClaimedMoyskladImport(context.Background(), task, ms, fp, domain.ImportCounts{}, time.Second)
	} else {
		err = proxyDB.CommitClaimedOzonImport(context.Background(), task, snap, fingerprint, domain.ImportCounts{}, time.Second)
	}
	if !errors.Is(err, postgres.ErrImportCommitUnknown) {
		t.Fatalf("commit was not reported uncertain: %v", err)
	}
	select {
	case <-dropped:
	case <-time.After(time.Second):
		t.Fatal("commit response was not dropped")
	}
	resolved, err := f.db.ResolveImportCommit(context.Background(), task)
	if err != nil || !resolved {
		t.Fatalf("committed result not reconciled: %v", err)
	}
	got, err := f.db.GetCatalogOperation(context.Background(), f.tenant, provider, f.account, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "SUCCEEDED" || got.BatchID == nil {
		t.Fatal("durable success missing after lost response")
	}
	var n int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE source=$2 AND account_id=$1`, f.account, provider).Scan(&n); err != nil || n != 1 {
		t.Fatal("unexpected duplicate batch")
	}
}

func newImportFixture(t *testing.T) importFixture {
	t.Helper()
	db, err := postgres.Open(context.Background(), os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	tenant, account := "import-tenant-"+suffix, "import-account-"+suffix
	if err = db.RegisterStockConnection(context.Background(), tenant, "ozon", account, "synthetic operator", false); err != nil {
		t.Fatal(err)
	}
	return importFixture{db, tenant, account}
}
func (f importFixture) submit(t *testing.T, key string, limits domain.ImportLimits) domain.ImportOperation {
	t.Helper()
	op, _, err := f.db.SubmitOzonImport(context.Background(), f.tenant, f.account, key, "synthetic-session", limits)
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func (f importFixture) claim(t *testing.T) domain.Task {
	t.Helper()
	task, err := f.db.Claim(context.Background(), time.Second)
	if err != nil || task == nil || task.Type != domain.OzonCatalogTask {
		t.Fatalf("claim: %v %+v", err, task)
	}
	return *task
}
func (f importFixture) inspect(t *testing.T, id string) domain.ImportOperation {
	t.Helper()
	op, err := f.db.GetImportOperation(context.Background(), f.tenant, f.account, id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func catalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("external write or unsupported method")
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/v3/product/list":
			var b struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
				Last string `json:"last_id"`
			}
			if json.NewDecoder(r.Body).Decode(&b) != nil {
				t.Error("request decode")
			}
			if b.Filter.Visibility == "ARCHIVED" {
				fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
			} else if b.Last == "" {
				fixtureText(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"one","archived":false}],"last_id":"next","total_items":2}}`)
			} else {
				fixtureText(t, w, `{"result":{"items":[{"product_id":2,"offer_id":"two","archived":false}],"total_items":2}}`)
			}
		case "/v2/warehouse/list":
			fixtureText(t, w, `{"warehouses":[{"warehouse_id":10,"name":"synthetic","is_rfbs":false}],"has_next":false}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			fixtureText(t, w, `{"products":[{"product_id":1,"offer_id":"one","warehouse_id":10,"present":0,"reserved":0,"free_stock":0}],"has_next":false}`)
		default:
			t.Errorf("unexpected marketplace method %s", r.URL.Path)
			w.WriteHeader(405)
		}
	}))
}
func runnerFor(t *testing.T, f importFixture, s *httptest.Server) ozonjob.Runner {
	t.Helper()
	c, err := ozon.New(ozon.Options{BaseURL: s.URL, ClientID: f.account, APIKey: "synthetic-only-key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	return ozonjob.Runner{Store: f.db, Account: f.account, Client: c}
}
func TestImportSubmissionIdempotencyScopeAndCancellation(t *testing.T) {
	f := newImportFixture(t)
	limits := domain.DefaultOzonImportLimits()
	var wg sync.WaitGroup
	ids := make(chan string, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, _, e := f.db.SubmitOzonImport(context.Background(), f.tenant, f.account, "same-key", "session", limits)
			ids <- o.ID
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate operation on concurrent replay")
		}
	}
	var operations, tasks int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1 AND account_id=$2),(SELECT count(*) FROM outbox_tasks WHERE import_tenant_id=$1 AND import_account_id=$2)`, f.tenant, f.account).Scan(&operations, &tasks); err != nil || operations != 1 || tasks != 1 {
		t.Fatal("idempotent submission duplicated durable state")
	}
	changed := limits
	changed.Pages--
	if _, _, err := f.db.SubmitOzonImport(context.Background(), f.tenant, f.account, "same-key", "session", changed); !errors.Is(err, postgres.ErrImportIdempotency) {
		t.Fatalf("changed payload: %v", err)
	}
	if _, _, err := f.db.SubmitOzonImport(context.Background(), f.tenant, f.account, "another", "session", limits); !errors.Is(err, postgres.ErrImportConflict) {
		t.Fatalf("overlapping import: %v", err)
	}
	if _, _, err := f.db.SubmitOzonImport(context.Background(), f.tenant+"-other", f.account, "other", "session", limits); !errors.Is(err, postgres.ErrImportScope) {
		t.Fatal("cross tenant accepted")
	}
	if _, err := f.db.GetImportOperation(context.Background(), f.tenant, f.account+"-other", first); !errors.Is(err, postgres.ErrImportNotFound) {
		t.Fatal("cross account exposed")
	}
	if _, _, err := f.db.ListImportOperations(context.Background(), f.tenant+"-other", f.account, 10, 0); !errors.Is(err, postgres.ErrImportScope) {
		t.Fatal("cross tenant list exposed")
	}
	if err := f.db.CancelQueuedImport(context.Background(), f.tenant, f.account, first); err != nil {
		t.Fatal(err)
	}
	op := f.inspect(t, first)
	if op.State != "CANCELLED" || op.Outcome == nil || *op.Outcome != "CANCELLED" || op.BatchID != nil {
		t.Fatalf("bad cancellation %+v", op)
	}
	if err := f.db.CancelQueuedImport(context.Background(), f.tenant, f.account, first); err != nil {
		t.Fatal("cancel retry", err)
	}
	var conflicts sync.WaitGroup
	type submissionResult struct {
		op  domain.ImportOperation
		err error
	}
	races := make(chan submissionResult, 2)
	for _, key := range []string{"race-a", "race-b"} {
		conflicts.Add(1)
		go func(key string) {
			defer conflicts.Done()
			o, _, err := f.db.SubmitOzonImport(context.Background(), f.tenant, f.account, key, "session", limits)
			races <- submissionResult{o, err}
		}(key)
	}
	conflicts.Wait()
	close(races)
	winner := ""
	rejected := 0
	for result := range races {
		if result.err == nil {
			if winner != "" {
				t.Fatal("two active imports accepted")
			}
			winner = result.op.ID
		} else if errors.Is(result.err, postgres.ErrImportConflict) {
			rejected++
		} else {
			t.Fatal(result.err)
		}
	}
	if winner == "" || rejected != 1 {
		t.Fatal("conflicting submissions did not serialize")
	}
	if err := f.db.CancelQueuedImport(context.Background(), f.tenant, f.account, winner); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE account_id=$1`, f.account).Scan(&n); err != nil || n != 0 {
		t.Fatal("cancelled operation imported data")
	}
}
func TestImportWorkerCompletesAndReusesEligibleBatch(t *testing.T) {
	f := newImportFixture(t)
	var mockBefore int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_tasks WHERE task_type='mock'`).Scan(&mockBefore); err != nil {
		t.Fatal(err)
	}
	s := catalogServer(t)
	defer s.Close()
	runner := runnerFor(t, f, s)
	limits := domain.DefaultOzonImportLimits()
	first := f.submit(t, "first", limits)
	task := f.claim(t)
	if err := runner.ProcessImport(context.Background(), task, time.Second); err != nil {
		t.Fatal(err)
	}
	op := f.inspect(t, first.ID)
	if op.State != "SUCCEEDED" || op.Outcome == nil || *op.Outcome != "COMPLETE_TRAVERSAL" || op.BatchID == nil || op.BatchCreated == nil || !*op.BatchCreated || op.Counts.Products != 2 || op.Counts.Warehouses != 1 || op.Counts.Observations != 1 || op.Counts.CompletedPages != 5 || op.Counts.CompletedRequests != 5 {
		t.Fatalf("incorrect completed import: %+v", op)
	}
	var n int
	err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM ozon_fbs_stock_observations WHERE import_id=$1 AND present=0 AND reserved=0 AND free_stock=0`, *op.BatchID).Scan(&n)
	if err != nil || n != 1 {
		t.Fatal("zero stock lost", err)
	}
	second := f.submit(t, "second", limits)
	task = f.claim(t)
	if err = runner.ProcessImport(context.Background(), task, time.Second); err != nil {
		t.Fatal(err)
	}
	repeated := f.inspect(t, second.ID)
	if repeated.State != "SUCCEEDED" || repeated.BatchID == nil || *repeated.BatchID != *op.BatchID || repeated.BatchCreated == nil || *repeated.BatchCreated {
		t.Fatalf("incorrect dedup: %+v", repeated)
	}
	err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE source='ozon' AND account_id=$1`, f.account).Scan(&n)
	if err != nil || n != 1 {
		t.Fatal("duplicate batch", err)
	}
	if err = f.db.CancelQueuedImport(context.Background(), f.tenant, f.account, second.ID); !errors.Is(err, postgres.ErrImportRunning) {
		t.Fatal("late cancellation overwrote success")
	}
	var mockAfter, pools, decisions, products, warehouses int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM outbox_tasks WHERE task_type='mock'),(SELECT count(*) FROM stock_pools WHERE tenant_id=$1),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),(SELECT count(*) FROM product_mappings WHERE tenant_id=$1),(SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1)`, f.tenant).Scan(&mockAfter, &pools, &decisions, &products, &warehouses); err != nil || mockAfter != mockBefore || pools != 0 || decisions != 0 || products != 0 || warehouses != 0 {
		t.Fatal("import created unrelated business effects", err)
	}
}
func TestImportLimitFailurePreservesPreviousBatch(t *testing.T) {
	f := newImportFixture(t)
	s := catalogServer(t)
	defer s.Close()
	runner := runnerFor(t, f, s)
	limits := domain.DefaultOzonImportLimits()
	first := f.submit(t, "complete", limits)
	if err := runner.ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
		t.Fatal(err)
	}
	previous := f.inspect(t, first.ID)
	limits.Pages = 1
	failed := f.submit(t, "limited", limits)
	if err := runner.ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
		t.Fatal(err)
	}
	op := f.inspect(t, failed.ID)
	if op.State != "FAILED" || op.Outcome == nil || *op.Outcome != "LIMIT_REACHED" || op.LimitingResource == nil || *op.LimitingResource != "pages" || op.BatchID != nil {
		t.Fatalf("incorrect limit outcome %+v", op)
	}
	var n int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE account_id=$1`, f.account).Scan(&n); err != nil || n != 1 {
		t.Fatal("partial data committed", err)
	}
	after := f.inspect(t, first.ID)
	if after.BatchID == nil || previous.BatchID == nil || *after.BatchID != *previous.BatchID {
		t.Fatal("prior completed batch changed")
	}
}
func TestImportLeaseFencingAndCommitReconciliation(t *testing.T) {
	f := newImportFixture(t)
	limits := domain.DefaultOzonImportLimits()
	op := f.submit(t, "lease", limits)
	old := f.claim(t)
	if err := f.db.CancelQueuedImport(context.Background(), f.tenant, f.account, op.ID); !errors.Is(err, postgres.ErrImportRunning) {
		t.Fatal("running cancellation accepted")
	}
	if err := f.db.ImportProgress(context.Background(), old, "PERSISTING", domain.ImportCounts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.inspect(t, op.ID); got.State != "OUTCOME_UNKNOWN" {
		t.Fatalf("expired persistence was not marked uncertain: %+v", got)
	}
	next := f.claim(t)
	if next.ID != old.ID || next.ClaimToken == old.ClaimToken || next.Attempts != 2 {
		t.Fatal("reclaim not fenced")
	}
	now := time.Now().UTC()
	snap := domain.OzonImport{Account: f.account, StartedAt: now, FinishedAt: now}
	fingerprint, err := application.OzonFingerprint(&snap)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.CommitClaimedOzonImport(context.Background(), old, snap, fingerprint, domain.ImportCounts{}, time.Second); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatalf("stale persistence accepted: %v", err)
	}
	if err = f.db.FailImport(context.Background(), old, "API_FAILURE", "", domain.ImportCounts{}); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatal("stale failure accepted")
	}
	if err = f.db.ImportProgress(context.Background(), old, "READING_STOCK", domain.ImportCounts{}); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatal("stale progress accepted")
	}
	if err = f.db.CommitClaimedOzonImport(context.Background(), next, snap, fingerprint, domain.ImportCounts{}, time.Second); err != nil {
		t.Fatal(err)
	}
	// Simulate losing the commit response: durable task+batch are found before any retry.
	ok, err := f.db.ResolveImportCommit(context.Background(), next)
	if err != nil || !ok {
		t.Fatal("committed operation not reconciled", err)
	}
	if err = f.db.CommitClaimedOzonImport(context.Background(), next, snap, fingerprint, domain.ImportCounts{}, time.Second); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatal("duplicate completion accepted")
	}
	done := f.inspect(t, op.ID)
	if done.State != "SUCCEEDED" || done.BatchID == nil {
		t.Fatal("completion lost")
	}
}
func TestImportAPIRejectsUnscopedAndUnacknowledgedRequests(t *testing.T) {
	for _, provider := range []string{"ozon", "moysklad"} {
		t.Run(provider, func(t *testing.T) { testCatalogImportAPI(t, provider) })
	}
}
func testCatalogImportAPI(t *testing.T, provider string) {
	f := newImportFixture(t)
	if provider == "moysklad" {
		if err := f.db.RegisterStockConnection(context.Background(), f.tenant, provider, f.account, "synthetic operator", false); err != nil {
			t.Fatal(err)
		}
	}
	field := "ozon_account"
	if provider == "moysklad" {
		field = "moysklad_account"
	}
	t.Setenv("MOYSKLAD_ACCOUNT_ID", f.account)
	t.Setenv("MOYSKLAD_TOKEN", "synthetic-secret-never-expose")
	t.Setenv("OZON_CLIENT_ID", f.account)
	t.Setenv("OZON_API_KEY", "synthetic-secret-never-expose")
	secret := strings.Repeat("s", 32)
	handler, err := console.Handler(f.db, secret, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	input := fmt.Sprintf(`{"tenant":%q,"ozon_account":%q,"operation_type":"OZON_CATALOG_IMPORT","idempotency_key":"api-key","acknowledge":true}`, f.tenant, f.account)
	if provider == "moysklad" {
		input = strings.ReplaceAll(input, "ozon_account", "moysklad_account")
		input = strings.ReplaceAll(input, domain.OzonCatalogImport, domain.MoyskladCatalogImport)
	}
	if w := call("POST", "/api/imports", input, "http://127.0.0.1:5173", nil); w.Code != 401 {
		t.Fatal("unauthenticated submission")
	}
	if w := call("GET", "/api/imports?tenant="+url.QueryEscape(f.tenant)+"&ozon_account="+url.QueryEscape(f.account), "", "", nil); w.Code != 401 {
		t.Fatal("unauthenticated import history exposed")
	}
	login := call("POST", "/api/session", fmt.Sprintf(`{"secret":%q}`, secret), "http://127.0.0.1:5173", nil)
	cookie := login.Result().Cookies()[0]
	if w := call("POST", "/api/imports", input, "http://evil.invalid", cookie); w.Code != 403 {
		t.Fatal("invalid origin accepted")
	}
	bad := strings.Replace(input, f.account, f.account+"-other", 1)
	if w := call("POST", "/api/imports", bad, "http://127.0.0.1:5173", cookie); w.Code != 403 {
		t.Fatal("wrong configured account accepted")
	}
	bad = strings.Replace(input, f.tenant, f.tenant+"-other", 1)
	if w := call("POST", "/api/imports", bad, "http://127.0.0.1:5173", cookie); w.Code != 404 {
		t.Fatal("unregistered tenant accepted")
	}
	bad = strings.Replace(input, `"acknowledge":true`, `"acknowledge":false`, 1)
	if w := call("POST", "/api/imports", bad, "http://127.0.0.1:5173", cookie); w.Code != 400 {
		t.Fatal("missing acknowledgement accepted")
	}
	bad = strings.ReplaceAll(strings.ReplaceAll(input, "OZON_CATALOG_IMPORT", "UNSUPPORTED_IMPORT"), "MOYSKLAD_CATALOG_IMPORT", "UNSUPPORTED_IMPORT")
	if w := call("POST", "/api/imports", bad, "http://127.0.0.1:5173", cookie); w.Code != 400 {
		t.Fatal("unsupported import accepted")
	}
	bad = strings.TrimSuffix(input, "}") + `,"api_key":"browser-secret"}`
	if w := call("POST", "/api/imports", bad, "http://127.0.0.1:5173", cookie); w.Code != 400 {
		t.Fatal("browser credential accepted")
	}
	created := call("POST", "/api/imports", input, "http://127.0.0.1:5173", cookie)
	if created.Code != 201 || strings.Contains(created.Body.String(), "synthetic-secret") || strings.Contains(created.Body.String(), secret) {
		t.Fatalf("submission or secrecy: %d", created.Code)
	}
	var result struct {
		Operation domain.ImportOperation `json:"operation"`
		Replay    bool                   `json:"idempotent_replay"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	replay := call("POST", "/api/imports", input, "http://127.0.0.1:5173", cookie)
	if replay.Code != 200 || !strings.Contains(replay.Body.String(), `"idempotent_replay":true`) {
		t.Fatal("submission replay failed")
	}
	changed := domain.DefaultOzonImportLimits()
	if provider == "moysklad" {
		changed = domain.DefaultMoyskladImportLimits()
	}
	changed.Pages--
	raw, _ := json.Marshal(changed)
	bad = strings.TrimSuffix(input, "}") + `,"limits":` + string(raw) + `}`
	if w := call("POST", "/api/imports", bad, "http://127.0.0.1:5173", cookie); w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal("different payload reused key")
	}
	scope := url.Values{"tenant": {f.tenant}, field: {f.account}}
	list := call("GET", "/api/imports?"+scope.Encode(), "", "", cookie)
	if list.Code != 200 || !strings.Contains(list.Body.String(), result.Operation.ID) || strings.Contains(list.Body.String(), "synthetic-secret") || strings.Contains(list.Body.String(), secret) {
		t.Fatal("scoped list missing")
	}
	scope.Set("tenant", f.tenant+"-other")
	if w := call("GET", "/api/imports/"+result.Operation.ID+"?"+scope.Encode(), "", "", cookie); w.Code != 404 {
		t.Fatal("cross tenant operation exposed")
	}
	scope.Set("tenant", f.tenant)
	scope.Set(field, f.account+"-other")
	if w := call("GET", "/api/imports/"+result.Operation.ID+"?"+scope.Encode(), "", "", cookie); w.Code != 404 {
		t.Fatal("cross account operation exposed")
	}
	cancelBody := fmt.Sprintf(`{"tenant":%q,"ozon_account":%q,"acknowledge":true}`, f.tenant, f.account)
	if provider == "moysklad" {
		cancelBody = strings.ReplaceAll(cancelBody, "ozon_account", "moysklad_account")
	}
	if w := call("POST", "/api/imports/"+result.Operation.ID+"/cancel", cancelBody, "http://127.0.0.1:5173", cookie); w.Code != 204 {
		t.Fatal("queued cancellation failed")
	}
	if provider == "moysklad" {
		cancelBody = strings.ReplaceAll(cancelBody, "ozon_account", "moysklad_account")
	}
	var second domain.ImportOperation
	var task domain.Task
	if provider == "moysklad" {
		second = submitMoy(t, f, "running", domain.DefaultMoyskladImportLimits())
		task = claimMoy(t, f, time.Second)
	} else {
		second = f.submit(t, "running", domain.DefaultOzonImportLimits())
		task = f.claim(t)
	}
	if w := call("POST", "/api/imports/"+second.ID+"/cancel", cancelBody, "http://127.0.0.1:5173", cookie); w.Code != 409 || !strings.Contains(w.Body.String(), "CANCELLATION_UNAVAILABLE") {
		t.Fatal("running cancellation accepted")
	}
	if err = f.db.FailImport(context.Background(), task, "API_FAILURE", "", domain.ImportCounts{}); err != nil {
		t.Fatal(err)
	}
}

func TestImportFailuresNeverPersistProviderResponse(t *testing.T) {
	f := newImportFixture(t)
	op := f.submit(t, "api-failure", domain.DefaultOzonImportLimits())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fixtureText(t, w, `{"code":"synthetic-secret-never-persist","customer":"private"}`)
	}))
	defer server.Close()
	if err := runnerFor(t, f, server).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
		t.Fatal(err)
	}
	got := f.inspect(t, op.ID)
	if got.Outcome == nil || *got.Outcome != "API_FAILURE" || got.BatchID != nil {
		t.Fatal("unexpected failure status")
	}
	var stored string
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT coalesce(last_error,'') FROM outbox_tasks WHERE import_operation_id=$1`, op.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "API_FAILURE" || strings.Contains(stored, "synthetic-secret") || strings.Contains(stored, "private") {
		t.Fatal("provider response persisted")
	}
}

func TestImportWorkerEmptyCatalogAndLimitVariants(t *testing.T) {
	for _, tc := range []struct {
		name               string
		adjust             func(*domain.ImportLimits)
		expected, resource string
	}{
		{"empty", func(*domain.ImportLimits) {}, "SUCCEEDED", ""},
		{"request-budget", func(l *domain.ImportLimits) { l.Requests = 1 }, "FAILED", "requests"},
		{"response-budget", func(l *domain.ImportLimits) { l.ResponseBytes = 8 }, "FAILED", "response_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Error("unexpected method")
				}
				switch r.URL.Path {
				case "/v3/product/list":
					fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
				case "/v2/warehouse/list":
					fixtureText(t, w, `{"warehouses":[],"has_next":false}`)
				default:
					t.Error("unexpected endpoint")
				}
			}))
			defer s.Close()
			limits := domain.DefaultOzonImportLimits()
			tc.adjust(&limits)
			op := f.submit(t, "operation", limits)
			if err := runnerFor(t, f, s).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
				t.Fatal(err)
			}
			result := f.inspect(t, op.ID)
			if result.State != tc.expected {
				t.Fatalf("state=%s", result.State)
			}
			if tc.expected == "SUCCEEDED" {
				if result.BatchID == nil || result.Counts.Products != 0 || result.Counts.CompletedPages != 3 {
					t.Fatal("empty traversal misrepresented")
				}
			} else if result.Outcome == nil || *result.Outcome != "LIMIT_REACHED" || result.LimitingResource == nil || *result.LimitingResource != tc.resource || result.BatchID != nil {
				t.Fatalf("invalid limit outcome %+v", result)
			}
		})
	}
}
func TestImportPersistenceErrorRollsBackBatch(t *testing.T) {
	f := newImportFixture(t)
	s := catalogServer(t)
	defer s.Close()
	runner := runnerFor(t, f, s)
	limits := domain.DefaultOzonImportLimits()
	first := f.submit(t, "first", limits)
	if err := runner.ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
		t.Fatal(err)
	}
	// Force a new fingerprint and fail inside the source-observation transaction.
	_, err := f.db.Pool.Exec(context.Background(), `CREATE FUNCTION test_ozon_operation_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.product_id='1' THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_ozon_operation_failure BEFORE INSERT ON ozon_product_observations FOR EACH ROW EXECUTE FUNCTION test_ozon_operation_failure()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_ozon_operation_failure ON ozon_product_observations; DROP FUNCTION IF EXISTS test_ozon_operation_failure()`)
	})
	// The second traversal differs in warehouse identity, so it must not deduplicate.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			var b struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.Filter.Visibility == "ARCHIVED" {
				fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
			} else {
				fixtureText(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"one"}],"total_items":1}}`)
			}
		case "/v2/warehouse/list":
			fixtureText(t, w, `{"warehouses":[],"has_next":false}`)
		case "/v2/product/info/stocks-by-warehouse/fbs":
			fixtureText(t, w, `{"products":[],"has_next":false}`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer other.Close()
	failed := f.submit(t, "second", limits)
	if err = runnerFor(t, f, other).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
		t.Fatal(err)
	}
	result := f.inspect(t, failed.ID)
	if result.State != "FAILED" || result.Outcome == nil || *result.Outcome != "PERSISTENCE_FAILURE" || result.BatchID != nil {
		t.Fatalf("wrong persistence failure %+v", result)
	}
	var n int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE account_id=$1`, f.account).Scan(&n); err != nil || n != 1 {
		t.Fatal("partial batch committed", err)
	}
	if old := f.inspect(t, first.ID); old.State != "SUCCEEDED" || old.BatchID == nil {
		t.Fatal("previous batch lost")
	}
}
func TestImportInFlightLeaseLossCannotCommitStaleBatch(t *testing.T) {
	f := newImportFixture(t)
	op := f.submit(t, "in-flight", domain.DefaultOzonImportLimits())
	old := f.claim(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/product/list" {
			blocked := false
			once.Do(func() { blocked = true; close(arrived) })
			if blocked {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
			return
		}
		if r.URL.Path == "/v2/warehouse/list" {
			fixtureText(t, w, `{"warehouses":[],"has_next":false}`)
			return
		}
		t.Error("unexpected path")
	}))
	defer server.Close()
	runner := runnerFor(t, f, server)
	done := make(chan error, 1)
	go func() { done <- runner.ProcessImport(context.Background(), old, 3*time.Second) }()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("first request not reached")
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	current := f.claim(t)
	if err := runner.ProcessImport(context.Background(), current, time.Second); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, postgres.ErrImportClaimLost) && !errors.Is(err, context.Canceled) {
			t.Fatalf("stale worker returned an unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stale worker failed to stop")
	}
	result := f.inspect(t, op.ID)
	if result.State != "SUCCEEDED" || result.Attempt != 2 || result.BatchID == nil {
		t.Fatalf("wrong winning state %+v", result)
	}
	var n int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE account_id=$1`, f.account).Scan(&n); err != nil || n != 1 {
		t.Fatal("stale worker persisted another batch", err)
	}
}
func TestImportWorkerDispatcherAndLeaseExhaustion(t *testing.T) {
	f := newImportFixture(t)
	server := catalogServer(t)
	defer server.Close()
	runner := runnerFor(t, f, server)
	op := f.submit(t, "worker", domain.DefaultOzonImportLimits())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (application.Worker{Store: f.db, Handler: application.MockHandler{}, Imports: runner, ImportAccount: f.account, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: 5 * time.Millisecond, Lease: time.Second, Retry: time.Millisecond}).Run(ctx)
	}()
	deadline := time.Now().Add(4 * time.Second)
	for f.inspect(t, op.ID).State != "SUCCEEDED" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("worker did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker leaked")
	}
	exhausted := f.submit(t, "exhausted", domain.DefaultOzonImportLimits())
	for i := 0; i < 3; i++ {
		task := f.claim(t)
		if _, err := f.db.Pool.Exec(context.Background(), `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.ExpireExhausted(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := f.inspect(t, exhausted.ID)
	if result.State != "FAILED" || result.Outcome == nil || *result.Outcome != "LEASE_EXHAUSTED" || result.BatchID != nil {
		t.Fatalf("exhaustion missing %+v", result)
	}
}

func TestImportProgressIsVisibleBeforeBatchCommit(t *testing.T) {
	f := newImportFixture(t)
	op := f.submit(t, "progress", domain.DefaultOzonImportLimits())
	task := f.claim(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
		case "/v2/warehouse/list":
			close(arrived)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fixtureText(t, w, `{"warehouses":[],"has_next":false}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	result := make(chan error, 1)
	go func() { result <- runnerFor(t, f, server).ProcessImport(context.Background(), task, time.Second) }()
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("traversal did not reach warehouse")
	}
	current := f.inspect(t, op.ID)
	if current.State != "RUNNING" || current.Phase != "READING_WAREHOUSES" || current.Counts.CompletedPages != 2 || current.Counts.CompletedRequests != 2 || current.BatchID != nil || current.ProgressAt == nil {
		t.Fatalf("invalid intermediate progress %+v", current)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if f.inspect(t, op.ID).State != "SUCCEEDED" {
		t.Fatal("import did not complete")
	}
}
func TestImportTimeoutAndUnavailableCredentialsAreSafe(t *testing.T) {
	for _, tc := range []struct {
		name         string
		wrongAccount bool
		outcome      string
	}{{"timeout", false, "API_FAILURE"}, {"account-mismatch", true, "CONFIGURATION_UNAVAILABLE"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			op := f.submit(t, "failure", domain.DefaultOzonImportLimits())
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				select {
				case <-r.Context().Done():
				case <-time.After(200 * time.Millisecond):
				}
			}))
			defer server.Close()
			client, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: f.account, APIKey: "synthetic", Timeout: 50 * time.Millisecond, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			account := f.account
			if tc.wrongAccount {
				account += "-other"
			}
			if err = (ozonjob.Runner{Store: f.db, Account: account, Client: client}).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
				t.Fatal(err)
			}
			result := f.inspect(t, op.ID)
			if result.State != "FAILED" || result.Outcome == nil || *result.Outcome != tc.outcome || result.BatchID != nil {
				t.Fatalf("wrong outcome %+v", result)
			}
			if tc.wrongAccount && calls.Load() != 0 {
				t.Fatal("mismatched account sent request")
			}
		})
	}
}

func TestImportDurationAndDatabaseTimeouts(t *testing.T) {
	t.Run("duration", func(t *testing.T) {
		f := newImportFixture(t)
		limits := domain.DefaultOzonImportLimits()
		limits.DurationNS = int64(100 * time.Millisecond)
		op := f.submit(t, "deadline", limits)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(300 * time.Millisecond):
			}
		}))
		defer server.Close()
		if err := runnerFor(t, f, server).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
			t.Fatal(err)
		}
		got := f.inspect(t, op.ID)
		if got.State != "FAILED" || got.Outcome == nil || *got.Outcome != "LIMIT_REACHED" || got.LimitingResource == nil || *got.LimitingResource != "duration" || got.BatchID != nil {
			t.Fatalf("duration not reported as limit: %+v", got)
		}
	})
	t.Run("database", func(t *testing.T) {
		f := newImportFixture(t)
		limits := domain.DefaultOzonImportLimits()
		limits.DatabaseTimeoutNS = 1
		op := f.submit(t, "database-deadline", limits)
		server := catalogServer(t)
		defer server.Close()
		if err := runnerFor(t, f, server).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
			t.Fatal(err)
		}
		got := f.inspect(t, op.ID)
		if got.State != "FAILED" || got.Outcome == nil || *got.Outcome != "PERSISTENCE_FAILURE" || got.BatchID != nil {
			t.Fatalf("database timeout left completed batch: %+v", got)
		}
		var n int
		if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_imports WHERE account_id=$1`, f.account).Scan(&n); err != nil || n != 0 {
			t.Fatal("database timeout committed partial batch")
		}
	})
}
func TestImportDoesNotAttachHiddenHistoricalBatch(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	tenant, account := "history-tenant-"+suffix, "history-account-"+suffix
	old := time.Now().Add(-time.Hour)
	snapshot := domain.OzonImport{Account: account, StartedAt: old, FinishedAt: old}
	fingerprint, err := application.OzonFingerprint(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := db.SaveOzonImport(ctx, snapshot, fingerprint); err != nil || !created {
		t.Fatal("historical fixture", err)
	}
	var oldID int64
	if err = db.Pool.QueryRow(ctx, `SELECT id FROM source_imports WHERE account_id=$1`, account).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if err = db.RegisterStockConnection(ctx, tenant, "ozon", account, "synthetic", false); err != nil {
		t.Fatal(err)
	}
	f := importFixture{db, tenant, account}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
		case "/v2/warehouse/list":
			fixtureText(t, w, `{"warehouses":[],"has_next":false}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	op := f.submit(t, "new", domain.DefaultOzonImportLimits())
	if err = runnerFor(t, f, server).ProcessImport(ctx, f.claim(t), time.Second); err != nil {
		t.Fatal(err)
	}
	v := f.inspect(t, op.ID)
	if v.BatchID == nil || *v.BatchID == oldID || v.BatchCreated == nil || !*v.BatchCreated {
		t.Fatal("ineligible historical batch was reused")
	}
}
func TestImportDatabaseGuardsTerminalIntegrity(t *testing.T) {
	f := newImportFixture(t)
	op := f.submit(t, "guards", domain.DefaultOzonImportLimits())
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE ozon_import_operations SET limits='{}' WHERE id=$1`, op.ID); err == nil {
		t.Fatal("limits were rewritten")
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE outbox_tasks SET state='succeeded' WHERE import_operation_id=$1`, op.ID); err == nil {
		t.Fatal("success without batch accepted")
	}
	if _, err := f.db.Pool.Exec(context.Background(), `DELETE FROM outbox_tasks WHERE import_operation_id=$1`, op.ID); err == nil {
		t.Fatal("durable task was deleted")
	}
	if err := f.db.CancelQueuedImport(context.Background(), f.tenant, f.account, op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE ozon_import_operations SET outcome='API_FAILURE' WHERE id=$1`, op.ID); err == nil {
		t.Fatal("terminal operation rewritten")
	}
}

func TestImportWorkersClaimOnlyConfiguredAccounts(t *testing.T) {
	first, second := newImportFixture(t), newImportFixture(t)
	op1 := first.submit(t, "first", domain.DefaultOzonImportLimits())
	op2 := second.submit(t, "second", domain.DefaultOzonImportLimits())
	if task, err := first.db.ClaimForWorker(context.Background(), time.Second, ""); err != nil || task != nil {
		t.Fatal("unconfigured worker claimed an import")
	}
	t2, err := first.db.ClaimForWorker(context.Background(), time.Second, second.account)
	if err != nil || t2 == nil || t2.ImportOperationID != op2.ID {
		t.Fatal("worker claimed a different account")
	}
	t1, err := second.db.ClaimForWorker(context.Background(), time.Second, first.account)
	if err != nil || t1 == nil || t1.ImportOperationID != op1.ID {
		t.Fatal("first account was globally blocked")
	}
	for _, task := range []*domain.Task{t1, t2} {
		if err = first.db.FailImport(context.Background(), *task, "CONFIGURATION_UNAVAILABLE", "", domain.ImportCounts{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImportPersistenceDoesNotDeadlockItsOwnHeartbeat(t *testing.T) {
	f := newImportFixture(t)
	server := catalogServer(t)
	defer server.Close()
	_, err := f.db.Pool.Exec(context.Background(), `CREATE FUNCTION test_ozon_operation_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.product_id='1' THEN PERFORM pg_sleep(0.3); END IF; RETURN NEW; END $$; CREATE TRIGGER test_ozon_operation_delay BEFORE INSERT ON ozon_product_observations FOR EACH ROW EXECUTE FUNCTION test_ozon_operation_delay()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_ozon_operation_delay ON ozon_product_observations; DROP FUNCTION IF EXISTS test_ozon_operation_delay()`)
	})
	op := f.submit(t, "slow-persist", domain.DefaultOzonImportLimits())
	task := f.claim(t)
	runner := runnerFor(t, f, server)
	runner.Store = &postgres.Store{Pool: f.db.Pool, Timeout: 80 * time.Millisecond}
	if err = runner.ProcessImport(context.Background(), task, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := f.inspect(t, op.ID); got.State != "SUCCEEDED" || got.BatchID == nil {
		t.Fatalf("own heartbeat interrupted persistence: %+v", got)
	}
}

func TestImportWorkerShutdownRecoversWithoutPartialBatch(t *testing.T) {
	f := newImportFixture(t)
	op := f.submit(t, "shutdown", domain.DefaultOzonImportLimits())
	task := f.claim(t)
	arrived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- runnerFor(t, f, server).ProcessImport(ctx, task, time.Second) }()
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected shutdown result: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
	interim := f.inspect(t, op.ID)
	if interim.State != "RUNNING" || interim.BatchID != nil {
		t.Fatalf("shutdown committed or finalized work: %+v", interim)
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	replacement := f.claim(t)
	s := catalogServer(t)
	defer s.Close()
	if err := runnerFor(t, f, s).ProcessImport(context.Background(), replacement, time.Second); err != nil {
		t.Fatal(err)
	}
	result := f.inspect(t, op.ID)
	if result.State != "SUCCEEDED" || result.Attempt != 2 || result.BatchID == nil {
		t.Fatalf("shutdown recovery failed: %+v", result)
	}
}

func TestImportRetryAttemptBudgetAndMalformedResponse(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		status                   int
		body, expected, resource string
		attempts                 int
	}{
		{"attempt-budget", 500, `{}`, "LIMIT_REACHED", "attempts", 2},
		{"malformed", 200, `{"private":"not persisted"}`, "INVALID_RESPONSE", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportFixture(t)
			limits := domain.DefaultOzonImportLimits()
			limits.Attempts = 1
			op := f.submit(t, "bounded", limits)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				fixtureText(t, w, tc.body)
			}))
			defer server.Close()
			client, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: f.account, APIKey: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: tc.attempts, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err = (ozonjob.Runner{Store: f.db, Account: f.account, Client: client}).ProcessImport(context.Background(), f.claim(t), time.Second); err != nil {
				t.Fatal(err)
			}
			got := f.inspect(t, op.ID)
			if got.State != "FAILED" || got.Outcome == nil || *got.Outcome != tc.expected || got.BatchID != nil || calls.Load() != 1 {
				t.Fatalf("wrong bounded failure %+v calls=%d", got, calls.Load())
			}
			if tc.resource != "" && (got.LimitingResource == nil || *got.LimitingResource != tc.resource) {
				t.Fatal("wrong limiting resource")
			}
		})
	}
}
