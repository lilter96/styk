//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type nativeMovementHTTPDocument struct {
	kind, id, account string
	body              map[string]any
}
type nativeMovementHTTPProvider struct {
	position     *positionHTTPProvider
	mu           sync.Mutex
	documents    map[string]*nativeMovementHTTPDocument
	writes       atomic.Int32
	requests     atomic.Int64
	loseResponse atomic.Bool
	shipped      atomic.Int64
	returned     atomic.Int64
	includedTax  bool
}
type nativeMovementHTTPFixture struct {
	positionHTTPFixture
	native *nativeMovementHTTPProvider
}

func newNativeMovementHTTPFixture(t *testing.T) nativeMovementHTTPFixture {
	t.Helper()
	return newNativeMovementHTTPFixtureWithTax(t, false)
}

func newNativeMovementHTTPFixtureWithTax(t *testing.T, includedTax bool) nativeMovementHTTPFixture {
	t.Helper()
	return newNativeMovementHTTPFixtureWithParent(t, includedTax, false)
}

func newNativeMovementHTTPFixtureWithParent(t *testing.T, includedTax, unposted bool) nativeMovementHTTPFixture {
	t.Helper()
	f := newPositionHTTPFixture(t)
	p := &nativeMovementHTTPProvider{position: f.provider, documents: map[string]*nativeMovementHTTPDocument{}, includedTax: includedTax}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	resolver, ok := f.factory.Credentials.(*credentials.Resolver)
	if !ok {
		t.Fatal("normal scoped credential resolver unavailable")
	}
	protector := orderprivacy.ActionProtector{Vault: resolver.Vault, Store: f.db}
	allow := func(scope orders.Scope) bool { return scope == f.config.Scope }
	var err error
	f.service, err = orderflow.NewService(f.db, f.factory, orderflow.ServiceOptions{AllowScope: allow, ActionProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	f.runner, err = orderflow.NewRunner(f.db, f.factory, orderflow.RunnerOptions{WorkerID: "native-http-worker", AllowScope: allow, Service: f.service, ActionReader: protector, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if !unposted {
		// This fixture exercises the applied-parent counter profile. The
		// independently observed unposted/zero-counter profile is separate.
		f.config.FieldMappings = slices.DeleteFunc(f.config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "applicable" })
		f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	}
	if includedTax {
		// Tax is observed in the destination, not inferred from an Ozon fraction.
		f.config.FieldMappings = slices.DeleteFunc(f.config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "positions.vat" })
	}
	f.config.NativeDocuments = &orders.NativeDocumentPolicy{Enabled: true, AllowPosted: true, Actions: []orders.Action{orders.ActionDemandCreate, orders.ActionSalesReturnCreate}, MaximumQuantity: "10", EventMaxAgeSeconds: 3600, AttestedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour)}
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "native-http-config"}, now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: map[string]string{}}, f.provider.base.server.URL, f.provider.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.merchant = &merchantBrowser{client: f.merchant.client, csrf: f.merchant.csrf, base: server.URL}
	first := f.preview(t)
	f.approve(t, first)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	t.Cleanup(func() {
		t.Logf("synthetic provider HTTP network attempts=%d; native write attempts=%d", p.requests.Load(), p.writes.Load())
	})
	return nativeMovementHTTPFixture{positionHTTPFixture: f, native: p}
}

func (f nativeMovementHTTPFixture) proposal(t *testing.T, action orders.Action, posted bool, event, demand string) orders.Preview {
	t.Helper()
	source := f.preview(t).Decisions[0].Posting
	kind := "draft_plan"
	if posted {
		kind = "shipment_dispatched"
	}
	if action == orders.ActionSalesReturnCreate && posted {
		kind = "return_received"
	}
	proposal := orders.NativeMovementProposal{PostingNumber: source.PostingNumber, Action: action, Applicable: orders.Present(posted), Lines: []orders.ActionQuantity{{Identity: source.Lines[0].Identity, Quantity: "1"}}, EventKind: kind, EventOccurredAt: time.Now().UTC().Truncate(time.Second), EventConfirmed: posted, PlannedMoment: time.Now().UTC().Add(time.Hour), EventReference: event, DemandWorkID: demand}
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{source.PostingNumber}, "native_proposals": []orders.NativeMovementProposal{proposal}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("native merchant preview HTTP %d %s", status, raw)
	}
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("native movement blocked: %+v", preview.Decisions[0].Validation)
	}
	return preview
}

func TestOrdersNativeHTTPDraftAndPostedDemandThenLinkedReturn(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	draft := f.proposal(t, orders.ActionDemandCreate, false, "native-http-draft-event", "")
	f.approve(t, draft)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	if f.native.shipped.Load() != 0 {
		t.Fatal("draft demand moved physical stock")
	}
	posted := f.proposal(t, orders.ActionDemandCreate, true, "native-http-shipment-event", "")
	f.approve(t, posted)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	demand := f.latestWork(t)
	if demand.Command.Action != orders.ActionDemandCreate {
		t.Fatal("native demand work missing")
	}
	returned := f.proposal(t, orders.ActionSalesReturnCreate, true, "native-http-return-event", demand.ID)
	f.approve(t, returned)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	if f.native.writes.Load() != 3 {
		t.Fatal("native workflow duplicated a document")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 3)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action IN ('demand_create','salesreturn_create') AND state='RECONCILED' AND dispatch_count=1`, 3)
	f.native.assertDocuments(t)
	f.assertIndependentNative(t)
	parent := f.readNative(t)
	if len(parent.Positions) != 2 || !parent.Positions[1].Reserve.IsKnown() || *parent.Positions[1].Reserve.Value != "2" || !parent.Positions[1].Shipped.IsKnown() || *parent.Positions[1].Shipped.Value != "0" {
		t.Fatal("native accounting workflow changed foreign reservation")
	}
}

func TestOrdersNativeHTTPLostCreateAcknowledgementOnlyReconciles(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	preview := f.proposal(t, orders.ActionDemandCreate, false, "native-http-lost-event", "")
	f.approve(t, preview)
	f.native.loseResponse.Store(true)
	f.run(t, orders.StateUnknown)
	f.run(t, orders.StateReconciled)
	f.assertVerifiedNativeHistory(t)
	if f.native.writes.Load() != 1 {
		t.Fatal("uncertain native creation was blindly retried")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_intents`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 1)
}

func (f nativeMovementHTTPFixture) assertIndependentNative(t *testing.T) {
	t.Helper()
	work := f.latestWork(t)
	binding, err := f.factory.ResolveOrder(f.ctx, work.Scope, "moysklad", work.Scope.MoyskladAccount, work.Credentials.Moysklad)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.NativeDocumentReader)
	if !ok {
		t.Fatal("real adapter lacks native readback")
	}
	correlation := work.Command.NativeMovement.Correlation
	correlation.DocumentID = work.Receipt.ExternalID
	actual, err := reader.ReadNativeDocument(f.ctx, orders.NativeDocumentRequest{Scope: work.Scope, Correlation: correlation})
	if err != nil || !orders.NativeDocumentMatches(work.Command, actual) {
		t.Fatalf("independent native readback: %v", err)
	}
	if !actual.DemandID.IsKnown() || !actual.Sum.IsKnown() || actual.Sum.Value.Amount != "12.34" {
		t.Fatal("linked return or exact monetary facts lost")
	}
}

func TestOrdersNativeHTTPStaleSourceAndForeignReservePreventDispatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(nativeMovementHTTPFixture)
	}{
		{"source changed", func(f nativeMovementHTTPFixture) { f.provider.change(3, 1234) }},
		{"foreign native reserve changed", func(f nativeMovementHTTPFixture) { f.editForeignReserve(t) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeMovementHTTPFixture(t)
			preview := f.proposal(t, orders.ActionDemandCreate, false, "native-http-stale-event", "")
			f.approve(t, preview)
			test.change(f)
			f.run(t, orders.StateBlocked)
			if f.native.writes.Load() != 0 || f.latestWork(t).DispatchCount != 0 {
				t.Fatal("stale native plan dispatched")
			}
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_intents`, 1)
		})
	}
}

func TestOrdersNativeHTTPWrongAccountCannotUseDemandBasis(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	preview := f.proposal(t, orders.ActionDemandCreate, true, "native-http-scope-event", "")
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	work := f.latestWork(t)
	for _, change := range []func(*orders.Scope){func(s *orders.Scope) { s.Tenant = "other" }, func(s *orders.Scope) { s.MoyskladAccount = ordersProcessID(440) }, func(s *orders.Scope) { s.OzonAccount = "999999" }} {
		scope := work.Scope
		change(&scope)
		if _, err := f.db.OrderNativeDocument(f.ctx, scope, work.ID); !errors.Is(err, orders.ErrNotFound) {
			t.Fatalf("cross-account demand basis disclosed: %v", err)
		}
	}
	body, err := json.Marshal(map[string]any{"tenant": "other", "moysklad_account": work.Scope.MoyskladAccount, "ozon_account": work.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{work.Command.PostingNumber}, "native_proposals": []orders.NativeMovementProposal{{PostingNumber: work.Command.PostingNumber, Action: orders.ActionSalesReturnCreate, Applicable: orders.Present(true), DemandWorkID: work.ID, Lines: []orders.ActionQuantity{{Identity: work.Command.NativeMovement.Lines[0].Identity, Quantity: "1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true); status != 404 {
		t.Fatalf("cross-tenant native proposal HTTP %d", status)
	}
	if f.native.writes.Load() != 1 {
		t.Fatal("cross-account request dispatched")
	}
}

func (p *nativeMovementHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)
	if p.stock(w, r) {
		return
	}

	if r.URL.Path == "/v3/posting/fbs/get" {
		capture := httptest.NewRecorder()
		p.position.serve(capture, r)
		var body map[string]any
		decoder := json.NewDecoder(capture.Body)
		decoder.UseNumber()
		if capture.Code != 200 || decoder.Decode(&body) != nil {
			w.WriteHeader(500)
			return
		}
		result := body["result"].(map[string]any)
		result["order_id"] = json.Number("5001")
		method := result["delivery_method"].(map[string]any)
		method["id"], method["warehouse_id"] = json.Number("4001"), json.Number("3001")
		for _, product := range result["products"].([]any) {
			row := product.(map[string]any)
			row["vat"], row["sku"] = "0", json.Number("2001")
		}
		p.position.base.reply(w, body)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/entity/demand") || strings.HasPrefix(r.URL.Path, "/entity/salesreturn") {
		p.native(w, r)
		return
	}
	if r.URL.Path == "/entity/product/"+ordersProcessID(8) {
		row := p.position.base.reference("entity/product", "product", ordersProcessID(8))
		row["id"], row["accountId"] = ordersProcessID(8), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
		row["archived"], row["isSerialTrackable"], row["trackingType"] = false, false, "NOT_TRACKED"
		p.position.base.reply(w, row)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && r.URL.Path != "/entity/customerorder/metadata" {
		capture := httptest.NewRecorder()
		p.position.serve(capture, r)
		if capture.Code != 200 {
			w.WriteHeader(capture.Code)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(capture.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			p.position.base.t.Error(err)
			w.WriteHeader(500)
			return
		}
		if rows, ok := body["rows"].([]any); ok {
			for index, item := range rows {
				row := item.(map[string]any)
				row["vatEnabled"] = false
				if index == 0 {
					row["shipped"] = json.Number(strconv.FormatInt(p.shipped.Load(), 10))
					if p.includedTax {
						row["vat"], row["vatEnabled"] = json.Number("20"), true
					}
				}
			}
		} else {
			body["vatEnabled"], body["vatIncluded"] = false, false
			if p.includedTax {
				body["vatEnabled"], body["vatIncluded"] = true, true
			}
			if _, specified := body["applicable"]; !specified {
				body["applicable"] = false
			}
		}
		p.position.base.reply(w, body)
		return
	}
	p.position.serve(w, r)
}

func (p *nativeMovementHTTPProvider) native(w http.ResponseWriter, r *http.Request) {
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
	if account == "" || account == r.Header.Get("Authorization") {
		w.WriteHeader(403)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/entity/"), "/")
	kind := parts[0]
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Method == http.MethodPost && len(parts) == 1 {
		p.writes.Add(1)
		p.create(w, r, account, kind)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	rows := make([]any, 0, len(p.documents))
	filter := r.URL.Query().Get("filter")
	for _, doc := range p.documents {
		if doc.account != account || doc.kind != kind {
			continue
		}
		if len(parts) == 1 {
			if filter == "" || strings.Contains(filter, "syncId="+doc.body["syncId"].(string)) && strings.Contains(filter, "externalCode="+doc.body["externalCode"].(string)) {
				rows = append(rows, p.document(doc))
			}
			continue
		}
		if parts[1] != doc.id {
			continue
		}
		if len(parts) == 2 {
			p.position.base.reply(w, p.document(doc))
			return
		}
		if len(parts) == 3 && parts[2] == "positions" {
			positions := p.positions(doc)
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			p.position.base.page(w, positions, len(positions), limit, 0)
			return
		}
	}
	if len(parts) == 1 {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		p.position.base.page(w, rows, len(rows), limit, 0)
		return
	}
	w.WriteHeader(404)
}

func (p *nativeMovementHTTPProvider) create(w http.ResponseWriter, r *http.Request, account, kind string) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	for _, doc := range p.documents {
		if doc.account == account && doc.kind == kind && doc.body["syncId"] == body["syncId"] {
			p.position.base.t.Error("duplicate native POST")
			w.WriteHeader(409)
			return
		}
	}
	if kind == "salesreturn" {
		if _, ok := body["demand"]; !ok {
			w.WriteHeader(400)
			return
		}
		if _, ok := body["customerOrder"]; ok {
			w.WriteHeader(400)
			return
		}
	}
	document := &nativeMovementHTTPDocument{kind: kind, id: ordersProcessID(55000 + len(p.documents)), account: account, body: body}
	p.documents[document.id] = document
	if kind == "demand" && body["applicable"] == true {
		p.shipped.Add(1)
	}
	if kind == "salesreturn" && body["applicable"] == true {
		p.returned.Add(1)
	}
	if p.loseResponse.Swap(false) {
		p.position.dropResponse(w)
		return
	}
	p.position.base.reply(w, p.document(document))
}

func (p *nativeMovementHTTPProvider) document(doc *nativeMovementHTTPDocument) map[string]any {
	row := p.position.base.reference("entity/"+doc.kind, doc.kind, doc.id)
	for key, value := range doc.body {
		row[key] = value
	}
	row["id"], row["accountId"], row["updated"] = doc.id, doc.account, "2026-11-04 12:00:00.000"
	row["positions"] = map[string]any{"meta": map[string]any{"href": p.position.base.server.URL + "/entity/" + doc.kind + "/" + doc.id + "/positions", "type": doc.kind + "position", "size": len(doc.body["positions"].([]any))}}
	row["sum"] = json.Number("1234")
	return row
}
func (p *nativeMovementHTTPProvider) positions(doc *nativeMovementHTTPDocument) []any {
	source := doc.body["positions"].([]any)
	result := make([]any, 0, len(source))
	for index, item := range source {
		row := map[string]any{}
		for key, value := range item.(map[string]any) {
			row[key] = value
		}
		id := ordersProcessID(56000 + index)
		row["id"], row["accountId"] = id, doc.account
		row["meta"] = p.position.base.reference("entity/"+doc.kind+"/"+doc.id+"/positions", doc.kind+"position", id)["meta"]
		result = append(result, row)
	}
	return result
}
func (p *nativeMovementHTTPProvider) assertDocuments(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.documents) != 3 {
		t.Fatal("native documents missing")
	}
	for _, doc := range p.documents {
		rows := doc.body["positions"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["quantity"] != json.Number("1") {
			t.Fatal("foreign native line entered movement")
		}
		if strings.Contains(fmt.Sprint(doc.body), "native-http-") {
			t.Fatal("private physical event reference leaked to provider")
		}
	}
}

func (p *nativeMovementHTTPProvider) stock(w http.ResponseWriter, r *http.Request) bool {
	base := p.position.base
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	item := base.reference("entity/product", "product", ordersProcessID(8))
	store := base.reference("entity/store", "store", ordersProcessID(4))
	quantity := json.Number(strconv.FormatInt(10-p.shipped.Load()+p.returned.Load(), 10))
	switch r.URL.Path {
	case "/entity/product":
		item["id"], item["accountId"], item["name"], item["article"] = ordersProcessID(8), account, "Synthetic process item", "process-offer"
		base.page(w, []any{item}, 1, limit, 0)
	case "/entity/variant":
		base.page(w, []any{}, 0, limit, 0)
	case "/report/stock/all":
		item["stock"], item["reserve"], item["inTransit"], item["quantity"], item["freeStock"] = quantity, json.Number("0"), json.Number("0"), quantity, quantity
		base.page(w, []any{item}, 1, limit, 0)
	case "/report/stock/bystore":
		store["stock"], store["reserve"], store["inTransit"] = quantity, json.Number("0"), json.Number("0")
		item["stockByStore"] = []any{store}
		base.page(w, []any{item}, 1, limit, 0)
	default:
		return false
	}
	return true
}

func (f nativeMovementHTTPFixture) stockImport(t *testing.T) int64 {
	t.Helper()
	client, err := f.factory.NewMoysklad(f.config.Scope.MoyskladAccount, "synthetic-orders-moysklad-"+f.config.Scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Coordinate(f.factory.Gate, providercall.Scope{Tenant: f.config.Scope.Tenant, Provider: "moysklad", Account: f.config.Scope.MoyskladAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Read(f.ctx, f.config.Scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.WarehouseStock) != 1 || snapshot.WarehouseStock[0].Stock == nil {
		t.Fatal("actual provider stock traversal absent")
	}
	fingerprint := "native-http-stock-" + *snapshot.WarehouseStock[0].Stock
	if _, err = f.db.SaveInventoryImport(f.ctx, snapshot, fingerprint); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1`, f.config.Scope.MoyskladAccount).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f nativeMovementHTTPFixture) nativeStockReason(t *testing.T, baseline int64, item string) string {
	t.Helper()
	request := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: f.config.Scope.Tenant, MoyskladAccount: f.config.Scope.MoyskladAccount, WarehouseID: f.config.StoreID, ItemKind: "product", ItemID: item}, Target: domain.StockTarget{OzonAccount: f.config.Scope.OzonAccount, ProductID: "1001", WarehouseID: "3001"}, Policy: domain.StockCalculationPolicy{Version: 1, MaxObservationAge: time.Hour, DecisionLifetime: time.Minute}}
	guard, err := f.db.OrderInventoryGuard(f.ctx, request, baseline, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, obligation := range guard.Obligations {
		if strings.HasPrefix(obligation.Reason, "NATIVE_DOCUMENT_") {
			return obligation.Reason
		}
	}
	return ""
}

func TestOrdersNativeHTTPPostedEffectFencesPoolUntilActualFreshStockRead(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	baseline := f.stockImport(t)
	preview := f.proposal(t, orders.ActionDemandCreate, true, "native-http-inventory-event", "")
	f.approve(t, preview)
	if reason := f.nativeStockReason(t, baseline, ordersProcessID(8)); reason != "NATIVE_DOCUMENT_EFFECT_PENDING" {
		t.Fatalf("approved physical event not fenced: %s", reason)
	}
	if reason := f.nativeStockReason(t, baseline, ordersProcessID(88)); reason != "" {
		t.Fatal("independent physical pool fenced")
	}
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	if reason := f.nativeStockReason(t, baseline, ordersProcessID(8)); reason != "NATIVE_DOCUMENT_STOCK_RECHECK_REQUIRED" {
		t.Fatalf("old baseline released after acknowledgment: %s", reason)
	}
	refreshed := f.stockImport(t)
	if refreshed == baseline {
		t.Fatal("changed actual stock snapshot missing")
	}
	if reason := f.nativeStockReason(t, refreshed, ordersProcessID(8)); reason != "NATIVE_DOCUMENT_STOCK_RECHECK_REQUIRED" {
		t.Fatalf("client-only snapshot released DB-clock fence: %s", reason)
	}
	admitted := nativeDurableStockImport(t, f)
	if reason := f.nativeStockReason(t, admitted, ordersProcessID(8)); reason != "" {
		t.Fatalf("durable worker traversal did not cover known effect: %s", reason)
	}
}
