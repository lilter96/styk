//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

type nativeLifecycleHTTPState struct {
	ID, Updated string
}

type nativeLifecycleHTTPFixture struct {
	nativeMovementHTTPFixture
	state          atomic.Pointer[nativeLifecycleHTTPState]
	demandLinks    atomic.Value
	reserveEffects atomic.Bool
	retainReserve  atomic.Bool
}

func newNativeLifecycleHTTPFixture(t *testing.T) *nativeLifecycleHTTPFixture {
	t.Helper()
	f := &nativeLifecycleHTTPFixture{nativeMovementHTTPFixture: newNativeMovementHTTPFixture(t)}
	f.provider.change(1, 1234)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.config.NativeDocuments.AllowPosted = false
	f.config.Lifecycle = &orders.LifecyclePolicy{Enabled: true, AutoDemand: true, AutoReturn: true, DemandMoment: orders.OnHandoffConfirmed, Mappings: []orders.MerchantCommandMapping{
		{StateID: ordersProcessID(20001), FromStateIDs: []string{f.config.StateID}, Action: orders.ActionDemandCreate, Scheme: "FBS", EventKind: "handoff_confirmed", WholePosting: true},
		{StateID: ordersProcessID(20002), FromStateIDs: []string{ordersProcessID(20001)}, Action: orders.ActionSalesReturnCreate, Scheme: "FBS", EventKind: "return_received", WholePosting: true},
	}}
	saved, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "native-lifecycle-opt-in", LifecycleConsent: true}, time.Now())
	if err != nil {
		t.Fatal("save independent native lifecycle consent", err)
	}
	f.config = saved
	f.state.Store(&nativeLifecycleHTTPState{ID: f.config.StateID, Updated: time.Now().Add(-time.Minute).In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
	f.demandLinks.Store("observed")
	f.provider.base.server.Config.Handler = http.HandlerFunc(f.serve)
	return f
}

func (f *nativeLifecycleHTTPFixture) serve(w http.ResponseWriter, r *http.Request) {
	if f.reserveResponse(w, r) {
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/entity/customerorder" && strings.HasPrefix(r.URL.Query().Get("filter"), "updated>=") {
		f.changes(w, r)
		return
	}
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/entity/customerorder/") || strings.Contains(r.URL.Path, "/positions") || strings.Contains(r.URL.Path, "/metadata") {
		f.native.serve(w, r)
		return
	}
	capture := httptest.NewRecorder()
	f.native.serve(capture, r)
	var body map[string]any
	decoder := json.NewDecoder(capture.Body)
	decoder.UseNumber()
	if capture.Code != http.StatusOK || decoder.Decode(&body) != nil {
		w.WriteHeader(capture.Code)
		return
	}
	state := f.state.Load()
	body["state"] = f.provider.base.reference("entity/customerorder/metadata/states", "state", state.ID)
	body["updated"] = state.Updated
	f.withDemandLinks(body)
	f.provider.base.reply(w, body)
}

func (f *nativeLifecycleHTTPFixture) withDemandLinks(body map[string]any) {
	switch f.demandLinks.Load().(string) {
	case "missing":
		delete(body, "demands")
	case "null":
		body["demands"] = nil
	case "foreign":
		body["demands"] = []any{f.provider.base.reference("entity/demand", "demand", ordersProcessID(20999))}
	default:
		links := make([]any, 0)
		f.native.mu.Lock()
		for _, document := range f.native.documents {
			if document.kind == "demand" {
				links = append(links, f.provider.base.reference("entity/demand", "demand", document.id))
			}
		}
		f.native.mu.Unlock()
		body["demands"] = links
	}
}

func (f *nativeLifecycleHTTPFixture) changes(w http.ResponseWriter, r *http.Request) {
	base := f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rows := make([]any, 0, 1)
	state := f.state.Load()
	window := strings.Split(r.URL.Query().Get("filter"), ";")
	for _, native := range base.native {
		if native.account != account || native.hidden || len(window) != 2 || state.Updated < strings.TrimPrefix(window[0], "updated>=") || state.Updated >= strings.TrimPrefix(window[1], "updated<") {
			continue
		}
		row := base.document(native)
		row["updated"] = state.Updated
		rows = append(rows, row)
	}
	base.page(w, rows, len(rows), limit, 0)
}

func (f *nativeLifecycleHTTPFixture) sellerTransition(t *testing.T, state string) {
	t.Helper()
	f.state.Store(&nativeLifecycleHTTPState{ID: state, Updated: time.Now().In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	if err := f.service.ProcessMerchantIntents(f.ctx, f.config); err != nil {
		t.Fatal("process observed physical seller transition", err)
	}
}

func (f *nativeLifecycleHTTPFixture) assertPhysicalStock(t *testing.T, wanted string) {
	t.Helper()
	batch := f.stockImport(t)
	var stock string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT stock::text FROM source_warehouse_stock_observations WHERE import_id=$1 AND external_id=$2 AND warehouse_id=$3`, batch, ordersProcessID(8), f.config.StoreID).Scan(&stock); err != nil || stock != wanted {
		t.Fatal("independent native stock readback", stock, wanted, err)
	}
}

func TestOrdersNativeAutomaticHTTPPhysicalHandoffAndReturn(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents`, 0)
	f.assertPhysicalStock(t, "10")
	f.sellerTransition(t, ordersProcessID(20001))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	if f.native.writes.Load() != 1 || f.native.shipped.Load() != 1 {
		t.Fatal("full physical handoff did not produce exactly one applied demand")
	}
	f.assertPhysicalStock(t, "9")
	f.sellerTransition(t, ordersProcessID(20001))
	if f.native.writes.Load() != 1 {
		t.Fatal("same seller state replayed the physical shipment")
	}
	f.sellerTransition(t, f.config.StateID)
	f.sellerTransition(t, ordersProcessID(20001))
	if f.native.writes.Load() != 1 {
		t.Fatal("new transition revision repeated already consumed shipment quantities")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents WHERE state='BLOCKED'`, 1)
	f.sellerTransition(t, ordersProcessID(20002))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.assertPhysicalStock(t, "10")
	if f.native.writes.Load() != 2 || f.native.returned.Load() != 1 {
		t.Fatal("full physical return did not create one bounded linked salesreturn")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 2)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE work->>'automatic'='true' AND action IN ('demand_create','salesreturn_create') AND state='RECONCILED' AND dispatch_count=1`, 2)
	f.assertIndependentNative(t)
}

func TestOrdersNativeAutomaticHTTPRevokedBeforeDispatch(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	f.sellerTransition(t, ordersProcessID(20001))
	f.config.Lifecycle.Enabled = false
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "native-lifecycle-revoke"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.run(t, orders.StateBlocked)
	if f.native.writes.Load() != 0 || f.native.shipped.Load() != 0 {
		t.Fatal("revoked native lifecycle performed an external write")
	}
	f.assertPhysicalStock(t, "10")
}

func TestOrdersNativeAutomaticHTTPLostAcknowledgementNeverResends(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	f.sellerTransition(t, ordersProcessID(20001))
	f.native.loseResponse.Store(true)
	f.run(t, orders.StateUnknown)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateReconciled)
	f.assertPhysicalStock(t, "9")
	if err := f.service.ProcessMerchantIntents(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	if f.native.writes.Load() != 1 || f.native.shipped.Load() != 1 || f.latestWork(t).DispatchCount != 1 {
		t.Fatal("restarting after uncertain automatic demand repeated its business effect")
	}
}

func collectNativeLifecycleChanges(t *testing.T, f *nativeLifecycleHTTPFixture) {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 0)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := binding.Client.(orderflow.SourceReader)
	if !ok {
		t.Fatal("production source reader unavailable")
	}
	collector, err := orderflow.NewIngestor(f.db, source, orderflow.IngestionOptions{WorkerID: "native-lifecycle-changes-worker", NativeResolver: f.factory, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = collector.CollectStream(f.ctx, f.config.Scope, orders.NativeChangesStream); err != nil {
		t.Fatal("collect bounded native change window", err)
	}
}

func TestOrdersNativeAutomaticHTTPUpdatedWindowFallback(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	collectNativeLifecycleChanges(t, f)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents`, 0)
	f.state.Store(&nativeLifecycleHTTPState{ID: ordersProcessID(20001), Updated: time.Now().Add(-3 * time.Second).In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
	collectNativeLifecycleChanges(t, f)
	collectNativeLifecycleChanges(t, f)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents`, 1)
	if err := f.service.ProcessMerchantIntents(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.assertPhysicalStock(t, "9")
	if f.native.writes.Load() != 1 {
		t.Fatal("overlapping updated window repeated native stock effect")
	}
}

func TestOrdersNativeAutomaticHTTPConcurrentSchedulers(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	f.state.Store(&nativeLifecycleHTTPState{ID: ordersProcessID(20001), Updated: time.Now().In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	var workers sync.WaitGroup
	errorsByWorker := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsByWorker <- f.service.ProcessMerchantIntents(f.ctx, f.config)
		}()
	}
	workers.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil && !errors.Is(err, orders.ErrConflict) {
			t.Fatal("concurrent native lifecycle scheduler", err)
		}
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE work->>'automatic'='true' AND action='demand_create'`, 1)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.assertPhysicalStock(t, "9")
	if f.native.writes.Load() != 1 {
		t.Fatal("two schedulers dispatched duplicate native movement")
	}
}

func TestOrdersNativeAutomaticHTTPExistingOrUnknownDemandLinksBlock(t *testing.T) {
	for _, tc := range []struct{ name, mode, code string }{
		{name: "omitted relationship", mode: "missing", code: "native_linked_demand_coverage_unavailable"},
		{name: "null relationship", mode: "null", code: "native_linked_demand_coverage_unavailable"},
		{name: "seller draft demand", mode: "foreign", code: "native_existing_linked_demand_requires_review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNativeLifecycleHTTPFixture(t)
			f.demandLinks.Store(tc.mode)
			collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
			f.sellerTransition(t, ordersProcessID(20001))
			items, err := f.db.OrderMerchantIntents(f.ctx, f.config.Scope, 10)
			if err != nil || len(items) != 1 || items[0].State != "BLOCKED" || items[0].Code != tc.code {
				t.Fatal("unproved demand absence did not require intervention", items, err)
			}
			if f.native.writes.Load() != 0 || f.native.shipped.Load() != 0 {
				t.Fatal("unknown or foreign linked demand caused automatic document creation")
			}
		})
	}
}
