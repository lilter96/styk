//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/postgres"
)

func customerBatch(f stockFixtureData, orders ...domain.CustomerOrderObservation) domain.CustomerOrderImport {
	now := time.Now().UTC()
	return domain.CustomerOrderImport{Tenant: f.req.Pool.Tenant, Account: f.req.Pool.MoyskladAccount, Since: now.Add(-time.Hour), Until: now, StartedAt: now, FinishedAt: now.Add(time.Second), Orders: orders}
}

func customerOrder(f stockFixtureData, id string, reserve *string) domain.CustomerOrderObservation {
	now := time.Now().UTC()
	wh := f.req.Pool.WarehouseID
	updated := "2026-10-29 12:00:00.000"
	yes := true
	qty := "3"
	shipped := "1"
	return domain.CustomerOrderObservation{ID: id, Account: f.req.Pool.MoyskladAccount, WarehouseID: &wh, Updated: &updated, Applicable: &yes, ObservedAt: now, Positions: []domain.CustomerOrderPosition{{ID: "position", AssortmentKind: "variant", AssortmentID: "item", Quantity: &qty, Reserve: reserve, Shipped: &shipped, ObservedAt: now}}}
}

func customerPosting(t *testing.T, f stockFixtureData, sku string) int64 {
	t.Helper()
	now := time.Now().UTC()
	p := postingFixture("posting-1", "awaiting_packaging", now, domain.PostingLine{OfferID: "offer", SKU: sku, Quantity: 3})
	p.WarehouseID = f.req.Target.WarehouseID
	batch, err := f.store.SavePostings(context.Background(), postingBatch(f.req.Pool.Tenant, f.req.Target.OzonAccount, now, p))
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = f.store.Pool.QueryRow(context.Background(), `SELECT id FROM ozon_posting_observations WHERE batch_id=$1`, batch).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCustomerOrderCorrelationAndStockDecision(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	zero := "0"
	order := customerOrder(f, "10000000-0000-0000-0000-000000000001", &zero)
	syncID := "20000000-0000-0000-0000-000000000001"
	order.SyncID = &syncID
	batch, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, order))
	if err != nil {
		t.Fatal(err)
	}
	posting := customerPosting(t, f, "sku")
	scope := postgres.OrderCorrelationScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
	corr, err := f.store.CorrelateCustomerOrder(ctx, scope, posting, batch)
	if err != nil || corr.Status != domain.OrderCandidateOnly || len(corr.Candidates) != 1 || len(corr.Candidates[0].Positions) != 1 || corr.Candidates[0].Positions[0].Reserve == nil || *corr.Candidates[0].Positions[0].Reserve != "0" || corr.Candidates[0].Positions[0].Shipped == nil || *corr.Candidates[0].Positions[0].Shipped != "1" || corr.Candidates[0].Positions[0].AssortmentKind != "variant" || corr.Candidates[0].Positions[0].ProductMappingVersion != 1 || corr.Candidates[0].WarehouseMappingVersion != 1 {
		t.Fatalf("candidate evidence: %+v %v", corr, err)
	}
	d := stockCalc(t, f)
	if d.Status != domain.StockUnknown || d.Quantity != nil || !stockHasReason(d, commitment.ProofAttribution) || !stockHasReason(d, "CUSTOMER_ORDER_BASELINE_INCLUSION_UNKNOWN") || !stockHasReason(d, "CUSTOMER_ORDER_CANDIDATE_ONLY") || d.Evidence.CustomerOrders == nil || len(d.Evidence.CustomerOrders.Correlations) != 1 {
		t.Fatalf("candidate promoted to stock proof: %+v", d)
	}
	if _, ok := d.Evidence.Proofs[commitment.ProofAttribution]; ok {
		t.Fatal("order correlation fabricated reserve attribution")
	}
	second := customerOrder(f, "10000000-0000-0000-0000-000000000002", nil)
	second.SyncID = &syncID
	deleted := "2026-10-29 13:00:00.000"
	second.Deleted = &deleted
	ambiguousBatch, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, order, second))
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := f.store.CorrelateCustomerOrder(ctx, scope, posting, ambiguousBatch)
	if err != nil || ambiguous.Status != domain.OrderAmbiguous || len(ambiguous.Candidates) != 2 || ambiguous.Candidates[1].SourceDeleted == nil || ambiguous.Candidates[1].Positions[0].Reserve != nil {
		t.Fatalf("ambiguity or nullable reserve lost: %+v %v", ambiguous, err)
	}
	var duplicates int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM moy_order_observations WHERE batch_id=$1 AND sync_id=$2`, ambiguousBatch, syncID).Scan(&duplicates); err != nil || duplicates != 2 {
		t.Fatal("duplicate external identifiers were collapsed")
	}
	unrelated := customerOrder(f, "10000000-0000-0000-0000-000000000004", &zero)
	unrelated.Positions[0].AssortmentID = "different-item"
	unrelatedBatch, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, unrelated))
	if err != nil {
		t.Fatal(err)
	}
	none, err := f.store.CorrelateCustomerOrder(ctx, scope, posting, unrelatedBatch)
	if err != nil || none.Status != domain.OrderNotFound || len(none.Candidates) != 0 {
		t.Fatalf("coincidental quantity or unrelated reserve matched: %+v %v", none, err)
	}
	missingWh := customerOrder(f, "10000000-0000-0000-0000-000000000003", nil)
	missingWh.WarehouseID = nil
	missingBatch, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, missingWh))
	if err != nil {
		t.Fatal(err)
	}
	missing, err := f.store.CorrelateCustomerOrder(ctx, scope, posting, missingBatch)
	if err != nil || missing.Status != domain.OrderInsufficient {
		t.Fatalf("unassigned warehouse inferred: %+v %v", missing, err)
	}
	conflictingPosting := customerPosting(t, f, "different-sku")
	conflict, err := f.store.CorrelateCustomerOrder(ctx, scope, conflictingPosting, batch)
	if err != nil || conflict.Status != domain.OrderConflict {
		t.Fatalf("SKU mismatch not reported: %+v %v", conflict, err)
	}
}

func TestCustomerOrderAppendOnlyRollbackAndConcurrency(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	two := "2"
	order := customerOrder(f, "10000000-0000-0000-0000-000000000010", &two)
	first, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, order))
	if err != nil {
		t.Fatal(err)
	}
	one := "1"
	order.Positions[0].Reserve = &one
	older := "2025-01-01 00:00:00.000"
	order.Updated = &older
	second, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, order))
	if err != nil || first == second {
		t.Fatalf("append: %v", err)
	}
	var firstReserve, secondReserve string
	if err = f.store.Pool.QueryRow(ctx, `SELECT (SELECT p.reserve::text FROM moy_order_positions p JOIN moy_order_observations o ON o.id=p.observation_id WHERE o.batch_id=$1),(SELECT p.reserve::text FROM moy_order_positions p JOIN moy_order_observations o ON o.id=p.observation_id WHERE o.batch_id=$2)`, first, second).Scan(&firstReserve, &secondReserve); err != nil || firstReserve != "2" || secondReserve != "1" {
		t.Fatalf("out-of-order history: %s %s %v", firstReserve, secondReserve, err)
	}
	if _, err = f.store.Pool.Exec(ctx, `DELETE FROM moy_order_batches WHERE id=$1`, first); err == nil {
		t.Fatal("order history mutable")
	}
	var firstObservation int64
	if err = f.store.Pool.QueryRow(ctx, `SELECT id FROM moy_order_observations WHERE batch_id=$1`, first).Scan(&firstObservation); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Pool.Exec(ctx, `INSERT INTO moy_order_positions(observation_id,tenant_id,account_id,position_id,observed_at) VALUES($1,$2,$3,'late-position',now())`, firstObservation, f.req.Pool.Tenant, f.req.Pool.MoyskladAccount)
	var sealed *pgconn.PgError
	if !errors.As(err, &sealed) || sealed.Code != "55000" {
		t.Fatalf("committed observation accepted a late position: %v", err)
	}
	posting := customerPosting(t, f, "sku")
	scope := postgres.OrderCorrelationScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
	old, err := f.store.CorrelateCustomerOrder(ctx, scope, posting, second)
	if err != nil || old.Status != domain.OrderConflict || len(old.Candidates) == 0 || !old.Candidates[0].HistoryConflict {
		t.Fatalf("out-of-order source revision treated as current: %+v %v", old, err)
	}
	sameTimestamp := customerOrder(f, order.ID, &one)
	conflicting, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, sameTimestamp))
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := f.store.CorrelateCustomerOrder(ctx, scope, posting, conflicting)
	if err != nil || conflict.Status != domain.OrderConflict || len(conflict.Candidates) == 0 || !conflict.Candidates[0].HistoryConflict {
		t.Fatalf("same-timestamp changed reserve ignored: %+v %v", conflict, err)
	}
	bad := customerOrder(f, "10000000-0000-0000-0000-000000000011", nil)
	bad.Positions = append(bad.Positions, bad.Positions[0])
	if _, err = f.store.SaveCustomerOrders(ctx, customerBatch(f, bad)); err == nil {
		t.Fatal("duplicate position committed")
	}
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM moy_order_observations WHERE tenant_id=$1 AND order_id=$2`, f.req.Pool.Tenant, bad.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left observation: %d %v", count, err)
	}
	const workers = 2
	var wg sync.WaitGroup
	ids := make(chan int64, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := f.store.SaveCustomerOrders(context.Background(), customerBatch(f, order))
			if e != nil {
				errs <- e
			} else {
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	seen := map[int64]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != workers {
		t.Fatalf("concurrent imports not appended: %v", seen)
	}
}

func TestCustomerOrderScopeIsolationAndForeignKeys(t *testing.T) {
	f, other := newStockFixture(t), newStockFixture(t)
	ctx := context.Background()
	id := "10000000-0000-0000-0000-000000000020"
	a, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, customerOrder(f, id, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.store.SaveCustomerOrders(ctx, customerBatch(other, customerOrder(other, id, nil))); err != nil {
		t.Fatal(err)
	}
	posting := customerPosting(t, f, "sku")
	for _, s := range []postgres.OrderCorrelationScope{{Tenant: other.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: other.req.Target.OzonAccount}, {Tenant: f.req.Pool.Tenant, MoyskladAccount: other.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}, {Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: other.req.Target.OzonAccount}} {
		if _, err = f.store.CorrelateCustomerOrder(ctx, s, posting, a); err == nil {
			t.Fatal("cross-scope correlation succeeded")
		}
	}
	_, err = f.store.Pool.Exec(ctx, `INSERT INTO moy_order_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status) VALUES($1,$2,now()-interval '1 hour',now(),now(),now(),'completed')`, other.req.Pool.Tenant, f.req.Pool.MoyskladAccount)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23503" {
		t.Fatalf("cross-tenant batch FK: %v", err)
	}
	var observation int64
	if err = f.store.Pool.QueryRow(ctx, `SELECT id FROM moy_order_observations WHERE batch_id=$1`, a).Scan(&observation); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Pool.Exec(ctx, `INSERT INTO moy_order_positions(observation_id,tenant_id,account_id,position_id,observed_at) VALUES($1,$2,$3,'other',now())`, observation, f.req.Pool.Tenant, other.req.Pool.MoyskladAccount)
	if !errors.As(err, &pe) || pe.Code != "23503" {
		t.Fatalf("cross-account position FK: %v", err)
	}
}

func TestCustomerOrderMockImportDoesNotPersistPII(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	account := f.req.Pool.MoyskladAccount
	id := "10000000-0000-0000-0000-000000000030"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("external write attempted")
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/entity/customerorder":
			fixturePrintf(t, w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":%q}]}`, id, account)
		case "/entity/customerorder/" + id:
			fixturePrintf(t, w, `{"id":%q,"accountId":%q,"agent":{"name":"PII_SENTINEL"},"shipmentAddress":"PII_SENTINEL","description":"PII_SENTINEL","externalCode":"PII_SENTINEL"}`, id, account)
		case "/entity/customerorder/" + id + "/positions":
			fixturePrintf(t, w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":"30000000-0000-0000-0000-000000000001","accountId":%q,"reserve":0,"quantity":1}]}`, account)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: fmt.Sprintf("mock-%d", time.Now().UnixNano()), Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	batch, err := (application.CustomerOrderImporter{Reader: client, Store: f.store}).Run(ctx, f.req.Pool.Tenant, account, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = f.store.Pool.QueryRow(ctx, `SELECT row_to_json(o)::text || row_to_json(p)::text FROM moy_order_observations o JOIN moy_order_positions p ON p.observation_id=o.id WHERE o.batch_id=$1`, batch).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PII_SENTINEL") {
		t.Fatal("sensitive response data persisted")
	}
}

func TestCustomerOrderInterruptedTraversalKeepsPreviousBatch(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	previous, err := f.store.SaveCustomerOrders(ctx, customerBatch(f))
	if err != nil {
		t.Fatal(err)
	}
	account := f.req.Pool.MoyskladAccount
	id := "10000000-0000-0000-0000-000000000040"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/entity/customerorder":
			if r.URL.Query().Get("offset") == "1" {
				http.Error(w, "temporary", http.StatusServiceUnavailable)
				return
			}
			fixturePrintf(t, w, `{"meta":{"size":2,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":%q}]}`, id, account)
		case "/entity/customerorder/" + id:
			fixturePrintf(t, w, `{"id":%q,"accountId":%q}`, id, account)
		case "/entity/customerorder/" + id + "/positions":
			fixtureText(t, w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: fmt.Sprintf("mock-%d", time.Now().UnixNano()), Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = (application.CustomerOrderImporter{Reader: client, Store: f.store}).Run(ctx, f.req.Pool.Tenant, account, now.Add(-time.Hour), now); err == nil {
		t.Fatal("interrupted traversal succeeded")
	}
	var batches, rows int
	if err = f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM moy_order_batches WHERE tenant_id=$1),(SELECT count(*) FROM moy_order_observations WHERE tenant_id=$1)`, f.req.Pool.Tenant).Scan(&batches, &rows); err != nil || batches != 1 || rows != 0 {
		t.Fatalf("partial import persisted: %d %d %v", batches, rows, err)
	}
	var exists bool
	if err = f.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM moy_order_batches WHERE id=$1)`, previous).Scan(&exists); err != nil || !exists {
		t.Fatal("previous completed batch was lost")
	}
}

func TestCustomerOrderUnregisteredScopeDoesNotContactSource(t *testing.T) {
	f := newStockFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	client, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: fmt.Sprintf("mock-%d", time.Now().UnixNano()), Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = (application.CustomerOrderImporter{Reader: client, Store: f.store}).Run(context.Background(), f.req.Pool.Tenant+"-foreign", f.req.Pool.MoyskladAccount, now.Add(-time.Hour), now)
	if err == nil || calls.Load() != 0 {
		t.Fatalf("unregistered scope contacted source: %v calls=%d", err, calls.Load())
	}
}
