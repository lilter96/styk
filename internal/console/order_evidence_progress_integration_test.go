//go:build integration

package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

func isolatedOrderEvidenceDB(t *testing.T) *postgres.Store {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || !strings.Contains(u.Path, "_test") {
		t.Skip("dedicated local test database required")
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, u.String(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("order_progress_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Pool.Exec(ctx, `CREATE SCHEMA `+name); err != nil {
		admin.Pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Pool.Exec(context.Background(), `DROP SCHEMA `+name+` CASCADE`); err != nil {
			t.Errorf("drop isolated order schema: %v", err)
		}
		admin.Pool.Close()
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	migrateCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(migrateCtx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", u.String(), "up")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migrate isolated order schema: %v\n%s", err, out)
	}
	db, err := postgres.Open(ctx, u.String(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	return db
}

type syntheticOrder struct {
	id      string
	posting string
	updated time.Time
}

func syntheticOrdersHandler(t *testing.T, account string, orders []syntheticOrder, writes *atomic.Int64) http.HandlerFunc {
	t.Helper()
	msk := time.FixedZone("MSK", 3*60*60)
	format := func(v time.Time) string { return v.In(msk).Format("2006-01-02 15:04:05.000") }
	respond := func(w http.ResponseWriter, v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Error(err)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/entity/customerorder":
			parts := strings.Split(r.URL.Query().Get("filter"), ";")
			if len(parts) != 2 {
				t.Error("invalid customer-order filter")
				http.Error(w, "bad filter", 400)
				return
			}
			from, err := time.ParseInLocation("2006-01-02 15:04:05.000", strings.TrimPrefix(parts[0], "updated>="), msk)
			if err != nil {
				t.Error(err)
				http.Error(w, "bad from", 400)
				return
			}
			to, err := time.ParseInLocation("2006-01-02 15:04:05.000", strings.TrimPrefix(parts[1], "updated<"), msk)
			if err != nil {
				t.Error(err)
				http.Error(w, "bad to", 400)
				return
			}
			rows := make([]map[string]any, 0)
			for _, o := range orders {
				if !o.updated.Before(from) && o.updated.Before(to) {
					rows = append(rows, map[string]any{"id": o.id, "accountId": account, "updated": format(o.updated)})
				}
			}
			offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
			if err != nil || offset < 0 || offset > len(rows) {
				t.Error("invalid customer-order offset")
				http.Error(w, "bad offset", 400)
				return
			}
			end := min(offset+3, len(rows))
			respond(w, map[string]any{"meta": map[string]any{"size": len(rows), "limit": 3, "offset": offset}, "rows": rows[offset:end]})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/entity/customerorder/"):
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/entity/customerorder/"), "/")
			for _, o := range orders {
				if o.id != parts[0] {
					continue
				}
				if len(parts) == 2 && parts[1] == "positions" {
					respond(w, map[string]any{"meta": map[string]any{"size": 1, "limit": 3, "offset": 0}, "rows": []any{map[string]any{"id": "30000000-0000-0000-0000-000000000001", "accountId": account, "reserve": 2, "quantity": 2}}})
					return
				}
				respond(w, map[string]any{"id": o.id, "accountId": account, "updated": format(o.updated), "applicable": true})
				return
			}
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v4/posting/fbs/list":
			var req struct {
				Cursor string `json:"cursor"`
				Filter struct {
					Since string `json:"since"`
					To    string `json:"to"`
				} `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				http.Error(w, "bad request", 400)
				return
			}
			from, err := time.Parse(time.RFC3339Nano, req.Filter.Since)
			if err != nil {
				t.Error(err)
				http.Error(w, "bad from", 400)
				return
			}
			to, err := time.Parse(time.RFC3339Nano, req.Filter.To)
			if err != nil {
				t.Error(err)
				http.Error(w, "bad to", 400)
				return
			}
			rows := make([]map[string]string, 0)
			for _, o := range orders {
				// Include the boundary in both slices to exercise safe deduplication.
				if !o.updated.Before(from) && !o.updated.After(to) {
					rows = append(rows, map[string]string{"posting_number": o.posting})
				}
			}
			offset := 0
			if req.Cursor != "" {
				offset, err = strconv.Atoi(req.Cursor)
				if err != nil || offset < 0 || offset > len(rows) {
					t.Error("bad cursor")
					http.Error(w, "bad cursor", 400)
					return
				}
			}
			end := min(offset+4, len(rows))
			cursor := ""
			if end < len(rows) {
				cursor = strconv.Itoa(end)
			}
			respond(w, map[string]any{"postings": rows[offset:end], "has_next": end < len(rows), "cursor": cursor})
		case r.Method == http.MethodPost && r.URL.Path == "/v3/posting/fbs/get":
			var req struct {
				PostingNumber string `json:"posting_number"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				http.Error(w, "bad request", 400)
				return
			}
			for i, o := range orders {
				if o.posting != req.PostingNumber {
					continue
				}
				status := "awaiting_packaging"
				if i%3 == 0 {
					status = "cancelled"
				}
				respond(w, map[string]any{"result": map[string]any{"posting_number": o.posting, "order_id": i + 1, "order_number": fmt.Sprint(i + 1), "status": status, "products": []any{map[string]any{"offer_id": "synthetic", "sku": 10, "quantity": 2}}}})
				return
			}
			http.NotFound(w, r)
		default:
			writes.Add(1)
			http.Error(w, "unexpected operation", http.StatusMethodNotAllowed)
		}
	}
}

func TestOrderEvidenceResumesBusyProviderWindows(t *testing.T) {
	ctx := context.Background()
	db := isolatedOrderEvidenceDB(t)
	var err error
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "progress-tenant-" + suffix, MoyskladAccount: "progress-moy-" + suffix, OzonAccount: "progress-ozon-" + suffix}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err = db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		_, err = db.Pool.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'synthetic',1,true,$4,now(),'synthetic-request')`, scope.Tenant, c.provider, c.account, make([]byte, 29))
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err := db.StartOrderEvidence(ctx, scope, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	frozenFrom, frozenTo := run.From, run.To
	orders := make([]syntheticOrder, 24)
	for i := range orders {
		orders[i] = syntheticOrder{id: fmt.Sprintf("10000000-0000-0000-0000-%012d", i+1), posting: fmt.Sprintf("synthetic-%02d", i), updated: run.From.Add(time.Duration(2*(i+1)) * time.Minute)}
	}
	var writes atomic.Int64
	server := httptest.NewServer(syntheticOrdersHandler(t, scope.MoyskladAccount, orders, &writes))
	defer server.Close()
	moy, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: "synthetic-only", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	market, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: scope.OzonAccount, APIKey: "synthetic-only", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	api := orderEvidenceAPI{db: db}
	cycles := 0
	for ; cycles < 50; cycles++ {
		run, err = db.StartOrderEvidence(ctx, scope, 1, time.Now().UTC())
		if err != nil || !run.From.Equal(frozenFrom) || !run.To.Equal(frozenTo) {
			t.Fatalf("frozen interval changed: %+v %v", run, err)
		}
		var sourceAttempts, postingAttempts int
		run, sourceAttempts, err = api.refreshSourceOrders(ctx, moy, run)
		if err != nil {
			t.Fatal(err)
		}
		run, postingAttempts, err = api.refreshMarketPostings(ctx, market, run)
		if err != nil {
			t.Fatal(err)
		}
		if sourceAttempts > 15 || postingAttempts > 15 {
			t.Fatalf("request budget exceeded: %d %d", sourceAttempts, postingAttempts)
		}
		var sourceCount, postingCount int
		if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM moy_order_batches WHERE tenant_id=$1), (SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1)`, scope.Tenant).Scan(&sourceCount, &postingCount); err != nil {
			t.Fatal(err)
		}
		if (run.SourceStatus != "COMPLETED" && sourceCount != 0) || (run.PostingStatus != "COMPLETED" && postingCount != 0) {
			t.Fatalf("partial traversal became complete: %d %d %+v", sourceCount, postingCount, run)
		}
		if run.SourceStatus == "COMPLETED" && run.PostingStatus == "COMPLETED" {
			break
		}
	}
	if cycles == 0 || cycles == 50 || run.SourceBatchID == nil || run.PostingBatchID == nil {
		t.Fatalf("busy traversal did not resume: cycles=%d source=%s posting=%s", cycles, run.SourceStatus, run.PostingStatus)
	}
	var ordersCount, reservesCount, postingCount, cancelled int
	err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM moy_order_observations WHERE batch_id=$1), (SELECT count(*) FROM moy_order_positions p JOIN moy_order_observations o ON o.id=p.observation_id WHERE o.batch_id=$1 AND p.reserve=2), (SELECT count(*) FROM ozon_posting_observations WHERE batch_id=$2), (SELECT count(*) FROM ozon_posting_observations WHERE batch_id=$2 AND status='cancelled')`, *run.SourceBatchID, *run.PostingBatchID).Scan(&ordersCount, &reservesCount, &postingCount, &cancelled)
	if err != nil || ordersCount != 24 || reservesCount != 24 || postingCount != 24 || cancelled != 8 || writes.Load() != 0 {
		t.Fatalf("incorrect complete observations: orders=%d reserves=%d postings=%d cancelled=%d writes=%d err=%v", ordersCount, reservesCount, postingCount, cancelled, writes.Load(), err)
	}
}

func TestOrderEvidenceChangedPostingStopsAndRestarts(t *testing.T) {
	ctx := context.Background()
	db := isolatedOrderEvidenceDB(t)
	var err error
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "conflict-tenant-" + suffix, MoyskladAccount: "conflict-moy-" + suffix, OzonAccount: "conflict-ozon-" + suffix}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err = db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		_, err = db.Pool.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'synthetic',1,true,$4,now(),'synthetic-request')`, scope.Tenant, c.provider, c.account, make([]byte, 29))
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err := db.StartOrderEvidence(ctx, scope, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !run.Posting.Split() {
		t.Fatal("split failed")
	}
	w := run.Posting.Pending[0]
	prior := domain.PostingImport{Tenant: scope.Tenant, Account: scope.OzonAccount, Since: w.From, Until: w.To, StartedAt: run.StartedAt, FinishedAt: time.Now().UTC(), Postings: []domain.PostingObservation{{PostingNumber: "same-posting", OrderID: "1", OrderNumber: "1", Status: "awaiting_packaging", ObservedAt: time.Now().UTC(), ObservedTimeBasis: "detail_received", Lines: []domain.PostingLine{{OfferID: "synthetic", SKU: "10", Quantity: 2}}}}}
	if err = run.Posting.Add(prior); err != nil {
		t.Fatal(err)
	}
	run, err = db.CheckpointOrderEvidence(ctx, run, "ozon", "IN_PROGRESS", "")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/posting/fbs/list":
			if _, err := fmt.Fprint(w, `{"postings":[{"posting_number":"same-posting"}],"has_next":false}`); err != nil {
				t.Error(err)
			}
		case "/v3/posting/fbs/get":
			if _, err := fmt.Fprint(w, `{"result":{"posting_number":"same-posting","order_id":1,"order_number":"1","status":"cancelled","products":[{"offer_id":"synthetic","sku":10,"quantity":2}]}}`); err != nil {
				t.Error(err)
			}
		default:
			t.Error("unexpected provider operation")
		}
	}))
	defer server.Close()
	market, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: scope.OzonAccount, APIKey: "synthetic-only", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	api := orderEvidenceAPI{db: db}
	run, _, err = api.refreshMarketPostings(ctx, market, run)
	if err != nil || run.PostingStatus != "CONFLICT" || run.PostingBatchID != nil {
		t.Fatalf("changed posting was not stopped: %+v %v", run, err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1`, scope.Tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("conflicting observations were sealed: %d %v", count, err)
	}
	oldGeneration := run.Generation
	run, err = db.StartOrderEvidence(ctx, scope, 1, time.Now().UTC())
	if err != nil || run.Generation <= oldGeneration || run.PostingStatus != "IN_PROGRESS" || len(run.Posting.Completed) != 0 {
		t.Fatalf("conflict did not start a fresh window: %+v %v", run, err)
	}
}

func TestOrderEvidenceOneConnectionPoolAndBoundedLocks(t *testing.T) {
	ctx := context.Background()
	db := isolatedOrderEvidenceDB(t)
	config := db.Pool.Config()
	config.MaxConns, config.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &postgres.Store{Pool: pool, Timeout: 2 * time.Second}
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "pool-tenant-" + suffix, MoyskladAccount: "pool-moy-" + suffix, OzonAccount: "pool-ozon-" + suffix}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err = store.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/entity/customerorder":
			if _, err := fmt.Fprint(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`); err != nil {
				t.Error(err)
			}
		case "/v4/posting/fbs/list":
			if _, err := fmt.Fprint(w, `{"postings":[],"has_next":false}`); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected provider endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	moy, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: "synthetic-only", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	market, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: scope.OzonAccount, APIKey: "synthetic-only", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	c := &connections{db: store, moy: moy, moyAccount: scope.MoyskladAccount, ozon: market, ozonAccount: scope.OzonAccount}
	api := orderEvidenceAPI{db: store, connections: c}
	body, err := json.Marshal(orderRefreshRequest{StockSyncScope: scope, WindowHours: 1})
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/stock-sync/order-evidence/refresh", bytes.NewReader(body)).WithContext(requestCtx)
	response := httptest.NewRecorder()
	api.refresh(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("one-connection refresh failed: %d %s", response.Code, response.Body.String())
	}
	var result orderRefreshResponse
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.SourceStatus != "COMPLETED" || result.PostingStatus != "COMPLETED" {
		t.Fatalf("one-connection result: %+v %v", result, err)
	}
	if pool.Stat().TotalConns() > 1 {
		t.Fatalf("pool exceeded configured capacity: %d", pool.Stat().TotalConns())
	}

	key := orderEvidenceLockKey(scope)
	release, err := lockOrderEvidence(ctx, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lockOrderEvidence(ctx, pool, key); !errors.Is(err, errOrderCheckBusy) {
		release()
		t.Fatalf("same scope was not locked: %v", err)
	}
	release()
	for i := 0; i < cap(orderEvidenceSlots); i++ {
		release, e := lockOrderEvidence(ctx, pool, fmt.Sprintf("other-scope-%d", i))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(release)
	}
	if _, err = lockOrderEvidence(ctx, pool, "one-over-capacity"); !errors.Is(err, errOrderCheckCapacity) {
		t.Fatalf("unbounded lock sessions: %v", err)
	}
}

func TestOrderEvidenceCredentialFailureDoesNotCreateRun(t *testing.T) {
	ctx := context.Background()
	db := isolatedOrderEvidenceDB(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "credential-tenant-" + suffix, MoyskladAccount: "credential-moy-" + suffix, OzonAccount: "credential-ozon-" + suffix}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,'moysklad',$2,'synthetic',1,false,NULL,now(),'synthetic-request')`, scope.Tenant, scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	c := &connections{db: db, moy: &moysklad.Client{}, moyAccount: scope.MoyskladAccount, ozon: &ozon.Client{}, ozonAccount: scope.OzonAccount}
	api := orderEvidenceAPI{db: db, connections: c}
	body, err := json.Marshal(orderRefreshRequest{StockSyncScope: scope, WindowHours: 1})
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		api.refresh(response, httptest.NewRequest(http.MethodPost, "/api/stock-sync/order-evidence/refresh", bytes.NewReader(body)))
		return response
	}
	response := refresh()
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "ORDER_CREDENTIAL_DISABLED") {
		t.Fatalf("disabled credential misreported: %d %s", response.Code, response.Body.String())
	}
	_, err = db.Pool.Exec(ctx, `UPDATE connection_credentials SET revision=2,enabled=true,ciphertext=$1 WHERE tenant_id=$2 AND provider='moysklad' AND account_id=$3`, make([]byte, 29), scope.Tenant, scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	response = refresh()
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "CREDENTIALS_UNAVAILABLE") {
		t.Fatalf("unavailable vault misreported: %d %s", response.Code, response.Body.String())
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM order_evidence_progress WHERE tenant_id=$1`, scope.Tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("credential failure created progress: %d %v", count, err)
	}
}

func TestOrderEvidenceLegacyLimitDoesNotSplit(t *testing.T) {
	ctx := context.Background()
	db := isolatedOrderEvidenceDB(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "legacy-tenant-" + suffix, MoyskladAccount: "legacy-moy-" + suffix, OzonAccount: "legacy-ozon-" + suffix}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	run, err := db.StartOrderEvidence(ctx, scope, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	orders := make([]syntheticOrder, 20)
	for i := range orders {
		orders[i] = syntheticOrder{id: fmt.Sprintf("10000000-0000-0000-0000-%012d", i+1), posting: fmt.Sprintf("legacy-%02d", i), updated: run.From.Add(time.Duration(i+1) * time.Minute)}
	}
	var writes atomic.Int64
	server := httptest.NewServer(syntheticOrdersHandler(t, scope.MoyskladAccount, orders, &writes))
	defer server.Close()
	moy, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: "synthetic-only", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	api := orderEvidenceAPI{db: db}
	run, attempts, err := api.refreshSourceOrders(ctx, moy, run)
	if err != nil || attempts != 15 || run.SourceStatus != "LIMIT_REACHED" || run.SourcePendingWindows != 1 || run.SourceCompletedWindows != 0 || run.SourceBatchID != nil {
		t.Fatalf("legacy limit was treated as resumable: %+v attempts=%d err=%v", run, attempts, err)
	}
	if writes.Load() != 0 {
		t.Fatal("provider mutation attempted")
	}
}
