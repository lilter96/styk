//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// This profile reflects independently observed MoySklad semantics: an applied
// linked demand changes physical stock while its unposted parent's shipped
// counter stays zero. It does not claim a real Ozon shipment was observed.
type unpostedNativeHTTPFixture struct {
	nativeMovementHTTPFixture
	profile *unpostedNativeHTTPProvider
}

func newUnpostedNativeHTTPFixture(t *testing.T) unpostedNativeHTTPFixture {
	t.Helper()
	f := newNativeMovementHTTPFixtureWithParent(t, false, true)
	f.config.NativeDocuments.AllowUnpostedParent = true
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "native-unposted-explicit-opt-in"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &unpostedNativeHTTPProvider{native: f.native}
	f.provider.base.server.Config.Handler = http.HandlerFunc(wrapper.serve)
	return unpostedNativeHTTPFixture{nativeMovementHTTPFixture: f, profile: wrapper}
}

type unpostedNativeHTTPProvider struct {
	native          *nativeMovementHTTPProvider
	hideDemand      atomic.Bool
	sourceCancelled atomic.Bool
	relatedPeer     atomic.Bool
}

func (p *unpostedNativeHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if p.independentSource(w, r) {
		return
	}
	if p.hiddenDemand(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		p.native.serve(w, r)
		return
	}
	capture := httptest.NewRecorder()
	p.native.serve(capture, r)
	if capture.Code != http.StatusOK {
		for k, v := range capture.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(capture.Code)
		if _, err := w.Write(capture.Body.Bytes()); err != nil {
			p.native.position.base.t.Error(err)
		}
		return
	}
	var body map[string]any
	decoder := json.NewDecoder(capture.Body)
	decoder.UseNumber()
	if decoder.Decode(&body) != nil {
		p.native.position.base.t.Error("unposted provider JSON")
		w.WriteHeader(500)
		return
	}
	p.parentZeroCounter(r, body)
	p.stockReserve(r, body)
	if strings.HasPrefix(r.URL.Path, "/entity/demand/") || strings.HasPrefix(r.URL.Path, "/entity/salesreturn/") {
		p.distinctNativePosition(r, body)
	}
	p.native.position.base.reply(w, body)
}

func (p *unpostedNativeHTTPProvider) parentZeroCounter(r *http.Request, body map[string]any) {
	if strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && strings.HasSuffix(r.URL.Path, "/positions") {
		for _, item := range objectRows(body) {
			item["shipped"] = json.Number("0")
		}
	}
}

func (p *unpostedNativeHTTPProvider) stockReserve(r *http.Request, body map[string]any) {
	if r.URL.Path == "/report/stock/all" {
		for _, item := range objectRows(body) {
			physical := 10 - p.native.shipped.Load() + p.native.returned.Load()
			item["reserve"], item["quantity"], item["freeStock"] = json.Number("3"), json.Number(strconv.FormatInt(physical-3, 10)), json.Number(strconv.FormatInt(physical-3, 10))
		}
	}
	if r.URL.Path == "/report/stock/bystore" {
		for _, item := range objectRows(body) {
			for _, store := range item["stockByStore"].([]any) {
				store.(map[string]any)["reserve"] = json.Number("3")
			}
		}
	}
}

func objectRows(body map[string]any) []map[string]any {
	raw, ok := body["rows"].([]any)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		rows = append(rows, item.(map[string]any))
	}
	return rows
}

func (p *unpostedNativeHTTPProvider) distinctNativePosition(r *http.Request, body map[string]any) {
	if !strings.HasSuffix(r.URL.Path, "/positions") {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 {
		return
	}
	id := strings.Replace(parts[2], "0000", "0001", 1)
	for index, row := range objectRows(body) {
		rowID := id
		if index != 0 {
			p.native.position.base.t.Error("unposted native fixture expects one owned line")
			return
		}
		row["id"] = rowID
		row["meta"] = p.native.position.base.reference("entity/"+parts[1]+"/"+parts[2]+"/positions", parts[1]+"position", rowID)["meta"]
	}
}

func TestOrdersNativeHTTPUnpostedParentUsesGrossDemandConsumption(t *testing.T) {
	f := newUnpostedNativeHTTPFixture(t)
	first := f.ship(t, "unposted-demand-first")
	f.assertSplitBlocked(t)
	f.assertUnpostedParent(t)
	f.assertDocumentAttribution(t, "9", true)
	f.assertProposalBlocked(t, orders.ActionDemandCreate, "unposted-demand-first", "")
	if f.native.writes.Load() != 1 {
		t.Fatal("duplicate event dispatched another native document")
	}
	second := f.ship(t, "unposted-demand-second")
	f.assertUnpostedParent(t)
	f.assertDocumentAttribution(t, "8", false)
	f.assertProposalBlocked(t, orders.ActionDemandCreate, "unposted-demand-third", "")
	f.receive(t, "unposted-return-first", first.ID)
	f.assertDocumentAttribution(t, "9", true)
	f.receive(t, "unposted-return-second", second.ID)
	f.assertDocumentAttribution(t, "10", true)
	f.assertUnpostedParent(t)
	f.assertProposalBlocked(t, orders.ActionDemandCreate, "unposted-redelivery-after-return", "")
	if f.native.writes.Load() != 4 {
		t.Fatal("bounded demands/returns created duplicate documents")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action IN ('demand_create','salesreturn_create') AND state='RECONCILED' AND dispatch_count=1`, 4)
	f.assertIndependentCancellationSettles(t)
	f.assertNativePointHistoryRetained(t)
	f.assertChildMirrorBlocked(t)
}

func (f unpostedNativeHTTPFixture) assertSplitBlocked(t *testing.T) {
	t.Helper()
	source := f.preview(t).Decisions[0].Posting
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{source.PostingNumber}, "proposals": []orders.ActionProposal{{PostingNumber: source.PostingNumber, Action: orders.ActionPostingSplitUnassembled}}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatal("split preview failed")
	}
	assertConsumedAllocationBlocked(t, preview.Decisions[0])
}

func assertConsumedAllocationBlocked(t *testing.T, decision orders.Decision) {
	t.Helper()
	if decision.Ready || len(decision.Commands) != 0 {
		t.Fatal("consumed allocation authorized another split or child reservation")
	}
	for _, issue := range decision.Validation {
		if issue.Code == "native_split_fulfillment_allocation_unverified" {
			return
		}
	}
	t.Fatal("consumed allocation blocker missing")
}

func (f unpostedNativeHTTPFixture) assertChildMirrorBlocked(t *testing.T) {
	t.Helper()
	f.profile.relatedPeer.Store(true)
	preview := f.peerPreview(t)
	assertConsumedAllocationBlocked(t, preview.Decisions[0])
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 4)
}

func (f unpostedNativeHTTPFixture) assertNativePointHistoryRetained(t *testing.T) {
	t.Helper()
	var before, after int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_native_document_reads`).Scan(&before); err != nil || before == 0 {
		t.Fatal("missing fresh document history", err)
	}
	for _, statement := range []string{`UPDATE orders_native_document_reads SET code=code`, `DELETE FROM orders_native_document_snapshots`} {
		if _, err := f.db.Pool.Exec(f.ctx, statement); err == nil {
			t.Fatal("immutable document evidence was changed")
		}
	}
	command := exec.CommandContext(f.ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", f.db.Pool.Config().ConnString(), "down")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "native document point history prevents rollback") {
		t.Fatal("migration rollback did not preserve native document evidence")
	}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_native_document_reads`).Scan(&after); err != nil || after != before {
		t.Fatal("guarded rollback altered document read history", err)
	}
}

func (f unpostedNativeHTTPFixture) ship(t *testing.T, event string) orders.Work {
	t.Helper()
	preview := f.proposal(t, orders.ActionDemandCreate, true, event, "")
	f.assertUnpostedAuthority(t, preview)
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	return f.latestWork(t)
}
func (f unpostedNativeHTTPFixture) receive(t *testing.T, event, demand string) {
	t.Helper()
	preview := f.proposal(t, orders.ActionSalesReturnCreate, true, event, demand)
	f.assertUnpostedAuthority(t, preview)
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
}
func (f unpostedNativeHTTPFixture) assertUnpostedParent(t *testing.T) {
	t.Helper()
	parent := f.readNative(t)
	if !parent.Fields.Applicable.IsKnown() || *parent.Fields.Applicable.Value || len(parent.Positions) != 2 || parent.Positions[0].ID != ordersProcessID(999) || !parent.Positions[0].Reserve.IsKnown() || *parent.Positions[0].Reserve.Value != "0" || !parent.Positions[0].Shipped.IsKnown() || *parent.Positions[0].Shipped.Value != "0" || parent.Positions[1].ID != ordersProcessID(1000) || !parent.Positions[1].Reserve.IsKnown() || *parent.Positions[1].Reserve.Value != "2" {
		t.Fatal("unposted or foreign native position changed")
	}
}
func (f unpostedNativeHTTPFixture) assertStock(t *testing.T, wanted string) int64 {
	t.Helper()
	batch := nativeDurableStockImport(t, f.nativeMovementHTTPFixture)
	var physical, reserve string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT stock::text,reserve::text FROM source_warehouse_stock_observations WHERE import_id=$1 AND warehouse_id=$2 AND external_id=$3`, batch, f.config.StoreID, ordersProcessID(8)).Scan(&physical, &reserve); err != nil {
		t.Fatal(err)
	}
	if !orders.ExactDecimalEqual(orders.Decimal(physical), orders.Decimal(wanted)) || !orders.ExactDecimalEqual(orders.Decimal(reserve), "3") {
		t.Fatalf("physical=%s reserve=%s; wanted physical=%s foreign reserve=3", physical, reserve, wanted)
	}
	return batch
}
func (f unpostedNativeHTTPFixture) assertProposalBlocked(t *testing.T, action orders.Action, event, demand string) {
	t.Helper()
	source := f.preview(t).Decisions[0].Posting
	proposal := orders.NativeMovementProposal{PostingNumber: source.PostingNumber, Action: action, Applicable: orders.Present(true), Lines: []orders.ActionQuantity{{Identity: source.Lines[0].Identity, Quantity: "1"}}, EventKind: "shipment_dispatched", EventOccurredAt: time.Now().UTC().Truncate(time.Second), EventConfirmed: true, EventReference: event, DemandWorkID: demand}
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{source.PostingNumber}, "native_proposals": []orders.NativeMovementProposal{proposal}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("blocked native preview HTTP%d %s", status, raw)
	}
	if preview.Decisions[0].Ready && len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("exhausted or duplicate event authorized a new document")
	}
}

func (p *unpostedNativeHTTPProvider) hiddenDemand(w http.ResponseWriter, r *http.Request) bool {
	if !p.hideDemand.Load() || r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/entity/demand") {
		return false
	}
	if r.URL.Path == "/entity/demand" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		p.native.position.base.page(w, []any{}, 0, limit, 0)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
	return true
}
func (p *unpostedNativeHTTPProvider) independentSource(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v3/posting/fbs/get" {
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		w.WriteHeader(400)
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var request struct {
		PostingNumber string `json:"posting_number"`
	}
	if json.Unmarshal(raw, &request) != nil {
		w.WriteHeader(400)
		return true
	}
	if request.PostingNumber == "position-http-posting" && p.sourceCancelled.Load() {
		capture := httptest.NewRecorder()
		p.native.serve(capture, r)
		var response map[string]any
		decoder := json.NewDecoder(capture.Body)
		decoder.UseNumber()
		if capture.Code != http.StatusOK || decoder.Decode(&response) != nil {
			p.native.position.base.t.Error("cancelled source fixture read failed")
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		row := response["result"].(map[string]any)
		row["status"], row["substatus"] = "cancelled", "posting_canceled"
		p.native.position.base.reply(w, response)
		return true
	}
	if request.PostingNumber != "native-unposted-independent" {
		return false
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(403)
		return true
	}
	row := p.native.position.base.posting(request.PostingNumber, "FBS")
	row["order_id"] = json.Number("5002")
	if p.relatedPeer.Load() {
		row["order_id"] = p.native.position.base.posting("position-http-posting", "FBS")["order_id"]
		row["parent_posting_number"] = "position-http-posting"
	}
	row["products"].([]any)[0].(map[string]any)["vat"] = "0"
	p.native.position.base.reply(w, map[string]any{"result": row})
	return true
}

func TestOrdersNativeHTTPUnpostedUnknownAllowsIndependentZeroReserveMirror(t *testing.T) {
	f := newUnpostedNativeHTTPFixture(t)
	preview := f.proposal(t, orders.ActionDemandCreate, true, "unposted-lost-native-event", "")
	f.approve(t, preview)
	f.profile.hideDemand.Store(true)
	f.native.loseResponse.Store(true)
	f.run(t, orders.StateUnknown)
	original := f.latestWork(t)
	f.assertProposalBlocked(t, orders.ActionDemandCreate, "unposted-after-unknown", "")
	peer := f.independentPreview(t)
	f.approve(t, peer)
	f.awaitLatest(t, orders.StateAcknowledged)
	f.awaitLatest(t, orders.StateReconciled)
	if f.native.writes.Load() != 1 {
		t.Fatal("unknown demand was replayed while unrelated zero-reserve mirror progressed")
	}
	var state string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT state FROM orders_work WHERE id=$1`, original.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(orders.StateUnknown) && state != string(orders.StatePolling) {
		t.Fatalf("lost native outcome fabricated: %s", state)
	}
}
func (f unpostedNativeHTTPFixture) independentPreview(t *testing.T) orders.Preview {
	t.Helper()
	preview := f.peerPreview(t)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("independent mirror blocked: %+v", preview.Decisions[0].Validation)
	}
	command := preview.Decisions[0].Commands[0]
	if command.Action != orders.ActionMirrorCreate || command.CustomerOrder == nil || len(command.CustomerOrder.Positions) != 1 || !command.CustomerOrder.Positions[0].Reserve.IsKnown() || *command.CustomerOrder.Positions[0].Reserve.Value != "0" {
		t.Fatal("independent path attempted inventory effects")
	}
	return preview
}

func (f unpostedNativeHTTPFixture) peerPreview(t *testing.T) orders.Preview {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "native-unposted-independent")
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, f.ordersStoreFixture, posting, "unposted-independent-source")
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{posting.PostingNumber}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("peer mirror preview HTTP%d %s", status, raw)
	}
	return preview
}
func (f unpostedNativeHTTPFixture) awaitLatest(t *testing.T, state orders.State) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := f.runner.RunNext(f.ctx); err != nil {
			t.Fatal(err)
		}
		history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if history.Operations[0].State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("independent normal worker did not reach %s", state)
}

func (f unpostedNativeHTTPFixture) assertUnpostedAuthority(t *testing.T, preview orders.Preview) {
	t.Helper()
	movement := preview.Decisions[0].Commands[0].NativeMovement
	if movement == nil || movement.ParentEffect != orders.NativeParentUnpostedDocument || !movement.ParentApplicable.IsKnown() || *movement.ParentApplicable.Value || movement.Consumption == nil || movement.Consumption.Digest == "" {
		t.Fatal("explicit unposted parent consumption authority missing")
	}
}

func (f unpostedNativeHTTPFixture) collectAllNativePoints(t *testing.T) {
	t.Helper()
	for range 8 {
		collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
		checkpoint, err := f.db.OrderCheckpoint(f.ctx, f.config.Scope, orders.NativePointStream)
		if err != nil {
			t.Fatal(err)
		}
		if checkpoint.Exhausted {
			return
		}
	}
	t.Fatal("normal parent/document point traversal did not exhaust its bounded pages")
}
func (f unpostedNativeHTTPFixture) assertDocumentAttribution(t *testing.T, physical string, blocked bool) {
	t.Helper()
	before := f.assertStock(t, physical)
	if guard := nativeAttributionGuard(t, f.nativeMovementHTTPFixture, before); !guard.Blocked {
		t.Fatal("fresh stock alone invented native document evidence")
	}
	f.collectAllNativePoints(t)
	if guard := nativeAttributionGuard(t, f.nativeMovementHTTPFixture, before); !guard.Blocked {
		t.Fatal("native document read admitted after stock was falsely covered by old traversal")
	}
	after := f.assertStock(t, physical)
	if guard := nativeAttributionGuard(t, f.nativeMovementHTTPFixture, after); guard.Blocked != blocked {
		t.Fatalf("fresh actual native document attribution blocked=%v want=%v: %+v", guard.Blocked, blocked, guard)
	}

}

func (f unpostedNativeHTTPFixture) assertIndependentCancellationSettles(t *testing.T) {
	t.Helper()
	f.profile.sourceCancelled.Store(true)
	f.provider.revision.Add(1)
	f.preview(t) // Independent fake Ozon cancellation enters through the normal reader.
	f.collectAllNativePoints(t)
	terminal := f.assertStock(t, "10")
	if guard := nativeAttributionGuard(t, f.nativeMovementHTTPFixture, terminal); guard.Blocked {
		t.Fatalf("fresh cancelled order remained blocked: %+v", guard)
	}
	f.assertUnpostedParent(t)
}
