//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

type discoveryFailureHTTP struct {
	base     *ordersProcessProvider
	mu       sync.Mutex
	pages    [][]string
	statuses map[string]int
	details  []string
}

func (p *discoveryFailureHTTP) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v4/posting/fbs/list" && r.URL.Path != "/v4/posting/fbs/unfulfilled/list" && r.URL.Path != "/v3/posting/fbs/get" {
		p.base.serve(w, r)
		return
	}
	var body struct {
		Cursor string `json:"cursor"`
		Number string `json:"posting_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(400)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if body.Number != "" {
		p.details = append(p.details, body.Number)
		if status := p.statuses[body.Number]; status != 0 {
			w.WriteHeader(status)
			return
		}
		p.base.reply(w, map[string]any{"result": p.base.posting(body.Number, "FBS")})
		return
	}
	index := 0
	if body.Cursor != "" {
		if _, err := fmt.Sscanf(body.Cursor, "page-%d", &index); err != nil {
			w.WriteHeader(400)
			return
		}
	}
	if index >= len(p.pages) {
		w.WriteHeader(400)
		return
	}
	rows := make([]any, 0, len(p.pages[index]))
	for _, number := range p.pages[index] {
		rows = append(rows, map[string]string{"posting_number": number})
	}
	next := ""
	if index+1 < len(p.pages) {
		next = fmt.Sprintf("page-%d", index+1)
	}
	p.base.reply(w, map[string]any{"postings": rows, "has_next": next != "", "cursor": next})
}

type discoveryFailureCollector struct {
	ingestor *orderflow.Ingestor
	stream   string
}

func (c discoveryFailureCollector) Collect(ctx context.Context, s orders.Scope) error {
	return c.ingestor.CollectStream(ctx, s, c.stream)
}

type discoveryFailureFixture struct {
	positionHTTPFixture
	transport *discoveryFailureHTTP
	ingestor  *orderflow.Ingestor
}

func newDiscoveryFailureFixture(t *testing.T, pages [][]string, statuses map[string]int) discoveryFailureFixture {
	t.Helper()
	f := newPositionHTTPFixture(t)
	provider := &discoveryFailureHTTP{base: f.provider.base, pages: pages, statuses: statuses}
	f.provider.base.server.Config.Handler = http.HandlerFunc(provider.serve)
	discoveryFailureMerchant(t, &f)
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := binding.Client.(orderflow.SourceReader)
	if !ok {
		t.Fatal("normal order source unavailable")
	}
	ingestor, err := orderflow.NewIngestor(f.db, source, orderflow.IngestionOptions{WorkerID: "discovery-http", PageSize: 100, ActiveBatch: 100})
	if err != nil {
		t.Fatal(err)
	}
	return discoveryFailureFixture{f, provider, ingestor}
}

func (f discoveryFailureFixture) collect(t *testing.T, stream string, wantError bool) orders.Checkpoint {
	t.Helper()
	raw, err := json.Marshal(ordersHTTPBody(f.config.Scope))
	if err != nil {
		t.Fatal(err)
	}
	status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/check", string(raw), true)
	if status != http.StatusOK {
		t.Fatalf("authenticated check HTTP%d", status)
	}
	collector := orderflow.Collector(discoveryFailureCollector{f.ingestor, stream})
	if stream == "" {
		collector = f.ingestor
		stream = orderflow.DiscoveryStream
	}
	runner, err := orderflow.NewRunner(f.db, f.factory, orderflow.RunnerOptions{WorkerID: "discovery-tick", Service: f.service, Collector: collector, AllowScope: func(s orders.Scope) bool { return s == f.config.Scope }})
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Tick(f.ctx)
	if (err != nil) != wantError {
		t.Fatalf("normal scheduled collector error=%v expectedError=%v", err, wantError)
	}
	checkpoint, err := f.db.OrderCheckpoint(f.ctx, f.config.Scope, stream)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Revision == 0 {
		t.Fatal("read failure lost durable page/checkpoint")
	}
	return checkpoint
}

func TestOrdersDiscoveryFailureIsolationHTTPPersistsCursorAndRepairsFirstEverKeys(t *testing.T) {
	f := newDiscoveryFailureFixture(t, [][]string{{"a-good", "b-missing", "c-good"}, {"d-good"}}, map[string]int{"b-missing": 404})
	first := f.collect(t, orderflow.DiscoveryStream, false)
	if first.Cursor != "page-1" || first.Complete || first.Exhausted || len(first.PageFailures) != 1 {
		t.Fatalf("partial page lost: %+v", first)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_posting_heads`, 2)
	f.mirrorGood(t, "a-good")
	second := f.collect(t, orderflow.DiscoveryStream, false)
	if !second.Exhausted || second.Complete || len(second.Failures) != 1 || len(second.PageFailures) != 0 {
		t.Fatal("exhausted listing falsely reported complete detail coverage")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_posting_heads`, 3)
	f.assertReadKeys(t, []string{"a-good", "b-missing", "c-good", "d-good"})
	f.assertMigrationHistory(t)
	// New window MUST retain the unrepaired key and never fabricate a head.
	third := f.collect(t, orderflow.DiscoveryStream, false)
	if third.Complete || len(third.Failures) != 1 || third.RefreshKey == second.RefreshKey {
		t.Fatal("new window erased old missing detail evidence")
	}
	f.transport.mu.Lock()
	delete(f.transport.statuses, "b-missing")
	f.transport.mu.Unlock()
	f.collect(t, orderflow.ActiveStream, false)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_read_repairs WHERE resolved_at IS NULL`, 0)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_posting_heads`, 4)
	final := f.collect(t, orderflow.DiscoveryStream, false)
	if !final.Complete || !final.Exhausted || len(final.Failures) > 0 {
		t.Fatalf("successful repaired traversal remains incomplete: %+v", final)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 1)
}

func (f discoveryFailureFixture) assertReadKeys(t *testing.T, want []string) {
	t.Helper()
	page, err := f.db.ActiveOrderPostings(f.ctx, f.config.Scope, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Postings) != len(want) {
		t.Fatalf("read key count %d", len(page.Postings))
	}
	for i, key := range page.Postings {
		if key.PostingNumber != want[i] {
			t.Fatal("repair keyset lost first-ever failed identity")
		}
		if key.PostingNumber == "b-missing" && (key.CommercialOrderID != "" || len(key.Lines) != 0) {
			t.Fatal("failed identity fabricated posting facts")
		}
	}
}

func (f discoveryFailureFixture) assertMigrationHistory(t *testing.T) {
	t.Helper()
	dsn := f.db.Pool.Config().ConnString()
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "down")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "order read-repair history prevents destructive rollback") {
		t.Fatal("read repair rollback discarded history")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_read_repairs`, 1)
}

func TestOrdersDiscoveryFailureIsolationHTTPGlobalFailureDefersAllListedKeys(t *testing.T) {
	f := newDiscoveryFailureFixture(t, [][]string{{"a-good", "b-down", "c-deferred", "d-deferred"}}, map[string]int{"b-down": 503})
	checkpoint := f.collect(t, "", true)
	if checkpoint.Complete || !checkpoint.Exhausted || len(checkpoint.PageFailures) != 3 {
		t.Fatalf("global failure did not retain every known identity: %+v", checkpoint)
	}
	if checkpoint.PageFailures[0].Code != "PROVIDER_UNAVAILABLE" || checkpoint.PageFailures[1].Code != "READ_DEFERRED" || checkpoint.PageFailures[2].Code != "READ_DEFERRED" {
		t.Fatal("unsafe failure classification")
	}
	f.transport.mu.Lock()
	calls := append([]string(nil), f.transport.details...)
	delete(f.transport.statuses, "b-down")
	f.transport.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("global failure did not stop provider phase: %v", calls)
	}
	f.collect(t, orderflow.ActiveStream, false)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_read_repairs WHERE resolved_at IS NULL`, 0)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_posting_heads`, 4)
}

func TestOrdersDiscoveryFailureIsolationMigrationUpgradeAndStartup(t *testing.T) {
	f := newOrdersStoreFixture(t)
	dsn := f.db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "61")
	if err := f.db.RequireCurrentSchema(f.ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatal("schema61 accepted by new binary")
	}
	catalogSyncMigrate(t, dsn, "up")
	if err := f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal(err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_configs`, 1)
}

func TestOrdersDiscoveryFailureIsolationKeepsMoreThanSummaryCapRepairable(t *testing.T) {
	pages := make([][]string, 2)
	statuses := make(map[string]int, 120)
	for i := 0; i < 120; i++ {
		number := fmt.Sprintf("failed-%03d", i)
		pages[i/60] = append(pages[i/60], number)
		statuses[number] = 404
	}
	f := newDiscoveryFailureFixture(t, pages, statuses)
	f.collect(t, orderflow.DiscoveryStream, false)
	checkpoint := f.collect(t, orderflow.DiscoveryStream, false)
	if checkpoint.Complete || !checkpoint.Exhausted || !checkpoint.FailuresTruncated || len(checkpoint.Failures) != 100 || len(checkpoint.PageFailures) != 60 {
		t.Fatal("bounded summary discarded actual page failures or claimed complete")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_read_repairs WHERE resolved_at IS NULL`, 120)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_posting_heads`, 0)
	first, err := f.db.ActiveOrderPostings(f.ctx, f.config.Scope, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Postings) != 100 || first.NextCursor == "" {
		t.Fatal("repair keyset first page lost")
	}
	second, err := f.db.ActiveOrderPostings(f.ctx, f.config.Scope, 100, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Postings) != 20 || second.NextCursor != "" || second.Postings[0].PostingNumber != "failed-100" {
		t.Fatal("repair summary cap starved later failed identities")
	}
}

// This uses actual merchant authentication and the normal preview/approval API;
// the partial account traversal cannot hide independently complete order facts.
func discoveryFailureMerchant(t *testing.T, f *positionHTTPFixture) {
	t.Helper()
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: map[string]string{}}, f.provider.base.server.URL, f.provider.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, err := identity.New(f.db.Pool, f.db.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	session, token, err := store.CreateSession(f.ctx, f.session.User)
	if err != nil {
		t.Fatal(err)
	}
	merchant := newMerchantBrowser(t, server.URL)
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	merchant.client.Jar.SetCookies(origin, []*http.Cookie{{Name: "styk_session", Value: token, Path: "/", HttpOnly: true}})
	merchant.csrf = session.CSRF
	f.merchant = merchant
	f.session = session
	f.ctx = identity.WithSession(f.ctx, session)
}

func (f discoveryFailureFixture) mirrorGood(t *testing.T, number string) {
	t.Helper()
	body := ordersHTTPBody(f.config.Scope)
	body["expected_revision"] = f.config.Revision
	body["posting_numbers"] = []string{number}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	status, response := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(raw), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(response, &preview) != nil || len(preview.Decisions) != 1 || !preview.Decisions[0].Ready {
		t.Fatalf("partial traversal blocked independent mirror: HTTP%d %+v", status, preview.Decisions)
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE state='RECONCILED' AND dispatch_count=1`, 1)
}
