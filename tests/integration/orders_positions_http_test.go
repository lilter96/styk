//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprovider"
	"github.com/lilter96/styk/internal/orders"
)

type positionHTTPProvider struct {
	base              *ordersProcessProvider
	quantity          atomic.Int64
	patches           atomic.Int32
	price             atomic.Int64
	revision          atomic.Int64
	ingests           atomic.Int64
	losePatchResponse atomic.Bool
	lastPatch         map[string]any
}

type positionHTTPFixture struct {
	ordersStoreFixture
	provider *positionHTTPProvider
	factory  orderprovider.Factory
	service  *orderflow.Service
	runner   *orderflow.Runner
	merchant *merchantBrowser
}

func TestOrdersPositionHTTPUsesNormalOutboxWorkerAndPreservesForeignReserve(t *testing.T) {
	f, next := positionHTTPUpdate(t)
	f.approve(t, next)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 2)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_reconciliations`, 2)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_position_update' AND state='RECONCILED' AND dispatch_count=1`, 1)
	f.assertNative(t)
	f.assertPatch(t)
	f.assertObservedNative(t)
	noop := f.preview(t)
	if !noop.Decisions[0].Ready || len(noop.Decisions[0].Commands) != 0 {
		t.Fatalf("unchanged native data not a no-op: %+v", noop.Decisions[0].Validation)
	}
	if ran, err := f.runner.RunNext(f.ctx); err != nil || ran {
		t.Fatalf("completed position intent replayed: %v %v", ran, err)
	}
	if f.provider.patches.Load() != 1 {
		t.Fatal("duplicate position dispatch")
	}
}

func positionHTTPUpdate(t *testing.T) (positionHTTPFixture, orders.Preview) {
	t.Helper()
	f := newPositionHTTPFixture(t)
	first := f.preview(t)
	if first.Decisions[0].Commands[0].Action != orders.ActionMirrorCreate {
		t.Fatal("expected normal native creation")
	}
	f.approve(t, first)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	f.provider.change(1, 1999)
	next := f.preview(t)
	if len(next.Decisions[0].Commands) != 1 || next.Decisions[0].Commands[0].Action != orders.ActionMirrorPositionUpdate {
		t.Fatalf("position decision blocked: %+v", next.Decisions[0].Validation)
	}
	patch := next.Decisions[0].Commands[0].PositionPatch
	if patch == nil || patch.PositionID != ordersProcessID(999) {
		t.Fatal("exact native position UUID lost")
	}
	return f, next
}

func TestOrdersPositionHTTPRejectsStaleSourceOrNativeEdit(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(positionHTTPFixture)
	}{
		{"source changed", "FRESH_SOURCE_CHANGED_OR_UNAVAILABLE", func(f positionHTTPFixture) { f.provider.change(3, 1999) }},
		{"foreign native reserve changed", "FRESH_TARGET_CHANGED_OR_UNAVAILABLE", func(f positionHTTPFixture) { f.editForeignReserve(t) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, preview := positionHTTPUpdate(t)
			f.approve(t, preview)
			test.change(f)
			f.run(t, orders.StateBlocked)
			work := f.latestWork(t)
			if work.Code != test.code || work.DispatchCount != 0 || f.provider.patches.Load() != 0 {
				t.Fatalf("stale decision dispatched: code=%s count=%d", work.Code, work.DispatchCount)
			}
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 1)
		})
	}
}

func TestOrdersPositionHTTPLostAcknowledgmentNeverResends(t *testing.T) {
	f, preview := positionHTTPUpdate(t)
	f.approve(t, preview)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	if f.provider.patches.Load() != 1 || f.latestWork(t).DispatchCount != 1 {
		t.Fatal("lost response did not retain dispatched UNKNOWN")
	}
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateReconciled)
	f.assertPatch(t)
	f.assertObservedNative(t)
	if f.provider.patches.Load() != 1 {
		t.Fatal("unknown position write was replayed")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 2)
}

func newPositionHTTPFixture(t *testing.T) positionHTTPFixture {
	t.Helper()
	db, vault := ordersProcessDB(t)
	session := ordersStoreOwner(t, db, "position-http")
	scope := ordersProcessSeed(t, db, vault, "position-http", 1)
	ctx := identity.WithSession(context.Background(), session)
	config := ordersProcessConfig(scope)
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: "constant.zero", Target: "positions.reserve"})
	config, err := db.SaveOrderConfig(ctx, orders.SaveConfigRequest{Config: config, Actor: session.AuditID, IdempotencyKey: "position-http-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	base := ordersProcessNewProvider(t)
	p := &positionHTTPProvider{base: base}
	p.change(2, 1234)
	base.add(scope.OzonAccount, "position-http-posting", "FBS", false)
	base.server.Config.Handler = http.HandlerFunc(p.serve)
	factory := ordersProcessFactory(t, db, base.server.URL)
	allow := func(s orders.Scope) bool { return s == scope }
	service, err := orderflow.NewService(db, factory, orderflow.ServiceOptions{AllowScope: allow})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := orderflow.NewRunner(db, factory, orderflow.RunnerOptions{WorkerID: "position-http-worker", AllowScope: allow, Service: service, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	fixture := positionHTTPFixture{ordersStoreFixture: ordersStoreFixture{db: db, ctx: ctx, session: session, config: config}, provider: p, factory: factory, service: service, runner: runner}
	fixture.merchant, fixture.session = labelHTTPMerchant(t, fixture.ordersStoreFixture)
	fixture.ctx = identity.WithSession(ctx, fixture.session)
	return fixture
}

func (f positionHTTPFixture) preview(t *testing.T) orders.Preview {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "position-http-posting")
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, f.ordersStoreFixture, posting, fmt.Sprintf("position-source-%d", f.provider.ingests.Add(1)))
	preview, err := f.service.Preview(f.ctx, orders.PreviewRequest{Scope: f.config.Scope, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, PostingNumbers: []string{posting.PostingNumber}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Decisions) != 1 {
		t.Fatal("missing posting decision")
	}
	return preview
}

func (f positionHTTPFixture) run(t *testing.T, state orders.State) {
	t.Helper()
	time.Sleep(2 * time.Millisecond)
	if ran, err := f.runner.RunNext(f.ctx); err != nil || !ran {
		t.Fatalf("normal worker phase: %v %v", ran, err)
	}
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if history.Operations[0].State != state {
		f.reportSource(t, history.Operations[0].Targets[0])
		t.Fatalf("worker state=%s code=%s", history.Operations[0].State, history.Operations[0].Targets[0].Code)
	}
}

func (f positionHTTPFixture) addForeignLine(t *testing.T) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.native) != 1 {
		t.Fatal("normal worker did not create exactly one document")
	}
	for _, native := range p.native {
		rows := native.body["positions"].([]any)
		rows = append(rows, map[string]any{"assortment": p.reference("entity/product", "product", ordersProcessID(98)), "quantity": json.Number("7"), "reserve": json.Number("2"), "price": json.Number("100"), "vat": json.Number("0")})
		native.body["positions"] = rows
	}
}

func (f positionHTTPFixture) assertNative(t *testing.T) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.native) != 1 {
		t.Fatal("position update created another customerorder")
	}
	for _, native := range p.native {
		rows := native.body["positions"].([]any)
		if len(rows) != 2 || rows[0].(map[string]any)["quantity"] != json.Number("1") || rows[1].(map[string]any)["reserve"] != json.Number("2") {
			t.Fatal("native source quantity or foreign reserve changed incorrectly")
		}
	}
}

func (p *positionHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v3/posting/fbs/get" {
		if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
			w.WriteHeader(403)
			return
		}
		row := p.base.posting("position-http-posting", "FBS")
		row["products"].([]any)[0].(map[string]any)["quantity"] = p.quantity.Load()
		minor := p.price.Load()
		row["products"].([]any)[0].(map[string]any)["price"] = fmt.Sprintf("%d.%02d", minor/100, minor%100)
		row["updated_at"] = time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC).Add(time.Duration(p.revision.Load()) * time.Minute).Format(time.RFC3339)
		p.base.reply(w, map[string]any{"result": row})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && r.URL.Path != "/entity/customerorder/metadata" {
		p.native(w, r)
		return
	}
	p.base.serve(w, r)
}

func (p *positionHTTPProvider) native(w http.ResponseWriter, r *http.Request) {
	base := p.base
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, native := range base.native {
		path := "/entity/customerorder/" + native.id
		if native.account != account || !strings.HasPrefix(r.URL.Path, path) {
			continue
		}
		rows := native.body["positions"].([]any)
		if r.Method == http.MethodPut {
			p.patch(w, r, native, rows)
			return
		}
		if r.URL.Path == path {
			doc := base.document(native)
			doc["positions"].(map[string]any)["meta"].(map[string]any)["size"] = len(rows)
			total, err := positionHTTPMinorTotal(rows)
			if err != nil {
				p.base.t.Error(err)
				w.WriteHeader(500)
				return
			}
			doc["sum"] = json.Number(strconv.FormatInt(total, 10))
			base.reply(w, doc)
			return
		}
		if r.URL.Path == path+"/positions" {
			result := make([]any, 0, len(rows))
			for index := range rows {
				result = append(result, p.position(native, index))
			}
			base.page(w, result, len(rows), 25, 0)
			return
		}
	}
	w.WriteHeader(404)
}

func (p *positionHTTPProvider) position(native *ordersProcessNative, index int) map[string]any {
	body := native.body["positions"].([]any)[index].(map[string]any)
	row := make(map[string]any, len(body)+5)
	for key, value := range body {
		row[key] = value
	}
	row["id"], row["accountId"] = ordersProcessID(999+index), native.account
	row["shipped"], row["discount"] = json.Number("0"), json.Number("0")
	row["meta"] = p.base.reference("entity/customerorder/"+native.id+"/positions", "customerorderposition", ordersProcessID(999+index))["meta"]
	return row
}

func (p *positionHTTPProvider) patch(w http.ResponseWriter, r *http.Request, native *ordersProcessNative, rows []any) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil || len(body) == 0 || len(body) > 3 || r.URL.Path != "/entity/customerorder/"+native.id+"/positions/"+ordersProcessID(999) {
		w.WriteHeader(400)
		return
	}
	for field, value := range body {
		if field != "quantity" && field != "price" && field != "reserve" {
			w.WriteHeader(400)
			return
		}
		if _, ok := value.(json.Number); !ok {
			w.WriteHeader(400)
			return
		}
	}
	rows = slices.Clone(rows)
	for field, value := range body {
		rows[0].(map[string]any)[field] = value
	}
	native.body["positions"] = rows
	p.lastPatch = body
	p.patches.Add(1)
	if p.losePatchResponse.Swap(false) {
		p.dropResponse(w)
		return
	}
	p.base.reply(w, p.position(native, 0))
}

func (p *positionHTTPProvider) dropResponse(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		p.base.t.Error("hijack synthetic lost acknowledgment")
		return
	}
	if err = conn.Close(); err != nil {
		p.base.t.Error("close synthetic lost acknowledgment")
	}
}

func (p *positionHTTPProvider) change(quantity, price int64) {
	p.quantity.Store(quantity)
	p.price.Store(price)
	p.revision.Add(1)
}

func (f positionHTTPFixture) approve(t *testing.T, preview orders.Preview) orders.Operation {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "preview_id": preview.ID, "acknowledge": true, "idempotency_key": "position-http-" + preview.ID})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	var operation orders.Operation
	if status != http.StatusOK || json.Unmarshal(raw, &operation) != nil || operation.ID == "" {
		t.Fatalf("merchant position approval: HTTP %d", status)
	}
	status, raw = f.merchant.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	var replay orders.Operation
	if status != http.StatusOK || json.Unmarshal(raw, &replay) != nil || replay.ID != operation.ID {
		t.Fatal("duplicate position confirmation created another operation")
	}
	return operation
}

func (f positionHTTPFixture) readNative(t *testing.T) orders.TargetObservation {
	t.Helper()
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(history.Operations) == 0 {
		t.Fatal("missing operation")
	}
	work := history.Operations[0].Targets[0]
	correlation := work.Command.Correlation
	if correlation.CustomerOrderID == "" {
		correlation.CustomerOrderID = work.Receipt.ExternalID
	}
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "moysklad", f.config.Scope.MoyskladAccount, work.Credentials.Moysklad)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.TargetReader)
	if !ok {
		t.Fatal("native reader unavailable")
	}
	target, err := reader.ReadCustomerOrder(f.ctx, correlation)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func (f positionHTTPFixture) assertPatch(t *testing.T) {
	t.Helper()
	p := f.provider
	p.base.mu.Lock()
	defer p.base.mu.Unlock()
	if len(p.lastPatch) != 2 || p.lastPatch["quantity"] != json.Number("1") || p.lastPatch["price"] != json.Number("1999") {
		t.Fatalf("unexpected native patch: %+v", p.lastPatch)
	}
}

func (f positionHTTPFixture) assertObservedNative(t *testing.T) {
	t.Helper()
	target := f.readNative(t)
	if !target.Complete || len(target.Positions) != 2 {
		t.Fatal("incomplete independent native readback")
	}
	for _, position := range target.Positions {
		if position.ID == ordersProcessID(999) {
			if position.Quantity != "1" || !position.Reserve.IsKnown() || *position.Reserve.Value != "0" || !position.Price.IsKnown() || position.Price.Value.Amount != "19.99" {
				t.Fatal("owned quantity/price/reserve mismatch")
			}
			continue
		}
		if position.ID != ordersProcessID(1000) || position.Quantity != "7" || !position.Reserve.IsKnown() || *position.Reserve.Value != "2" {
			t.Fatal("foreign line or reserve changed")
		}
	}
}

func (f positionHTTPFixture) reportSource(t *testing.T, work orders.Work) {
	t.Helper()
	preview, err := f.db.OrderPreview(f.ctx, work.Scope, work.PreviewID)
	if err != nil {
		t.Log("source preview diagnostics unavailable")
		return
	}
	binding, err := f.factory.ResolveOrder(f.ctx, work.Scope, "ozon", work.Scope.OzonAccount, work.Credentials.Ozon)
	if err != nil {
		t.Log("source binding diagnostics unavailable")
		return
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, work.Scope, work.Command.PostingNumber)
	if err != nil {
		t.Log("source fresh diagnostic read unavailable")
		return
	}
	t.Logf("source differing top-level fields: %v", ordersProcessSourceDifference(preview.Decisions[0].Posting, posting))
	t.Logf("source capability references reviewed=%+v fresh=%+v", preview.Decisions[0].Posting.DeliveryProfile, posting.DeliveryProfile)
}

func (f positionHTTPFixture) latestWork(t *testing.T) orders.Work {
	t.Helper()
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(history.Operations) == 0 || len(history.Operations[0].Targets) != 1 {
		t.Fatal("missing durable position operation")
	}
	return history.Operations[0].Targets[0]
}

func (f positionHTTPFixture) editForeignReserve(t *testing.T) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.native) != 1 {
		t.Fatal("missing native document")
	}
	for _, native := range p.native {
		native.body["positions"].([]any)[1].(map[string]any)["reserve"] = json.Number("3")
	}
}

func positionHTTPMinorTotal(rows []any) (int64, error) {
	var total int64
	for _, row := range rows {
		value := row.(map[string]any)
		quantity, err := strconv.ParseInt(string(value["quantity"].(json.Number)), 10, 64)
		if err != nil || quantity < 0 || quantity > 1000 {
			return 0, fmt.Errorf("synthetic quantity outside small integer fixture")
		}
		price, err := strconv.ParseInt(string(value["price"].(json.Number)), 10, 64)
		if err != nil || price < 0 || price > 1000000 {
			return 0, fmt.Errorf("synthetic price outside small integer fixture")
		}
		total += quantity * price
	}
	return total, nil
}

func (f positionHTTPFixture) restartedRunner(t *testing.T) *orderflow.Runner {
	t.Helper()
	runner, err := orderflow.NewRunner(f.db, f.factory, orderflow.RunnerOptions{WorkerID: "position-http-restarted", AllowScope: func(s orders.Scope) bool { return s == f.config.Scope }, Service: f.service, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}
