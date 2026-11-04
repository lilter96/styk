//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

type compositionHTTPProvider struct {
	base *positionHTTPProvider
	mode atomic.Int32
	puts atomic.Int32
	lost atomic.Bool
}

type compositionHTTPFixture struct {
	positionHTTPFixture
	composition *compositionHTTPProvider
}

func newCompositionHTTPFixture(t *testing.T) compositionHTTPFixture {
	t.Helper()
	f := newPositionHTTPFixture(t)
	p := &compositionHTTPProvider{base: f.provider}
	p.mode.Store(1)
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	config := f.config
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: "constant.false", Target: "applicable"}, orders.FieldMapping{Source: "constant.false", Target: "vat_enabled"}, orders.FieldMapping{Source: "constant.true", Target: "vat_included"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "composition-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	compositionSecondMapping(t, f)
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: map[string]string{}}, p.base.base.server.URL, p.base.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	// Cookie host is unchanged; this is a second normal API process sharing PG.
	f.merchant.base = server.URL
	return compositionHTTPFixture{f, p}
}

func compositionSecondMapping(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	now, rfbs := time.Now().UTC(), false
	source := domain.InventoryImport{Account: f.config.Scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: ordersProcessID(8), Name: "Synthetic first", Article: "process-offer"}, {Kind: "product", ID: ordersProcessID(9), Name: "Synthetic second", Article: "process-second"}, {Kind: "store", ID: ordersProcessID(4), Name: "Synthetic store"}}}
	market := domain.OzonImport{Account: f.config.Scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", SKU: "2001", OfferID: "process-offer"}, {ProductID: "1002", SKU: "2002", OfferID: "process-second"}}, Warehouses: []domain.OzonWarehouse{{ID: "3001", IsRFBS: &rfbs}}}
	if _, err := f.db.SaveInventoryImport(f.ctx, source, "composition-source-products"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SaveOzonImport(f.ctx, market, "composition-market-products"); err != nil {
		t.Fatal(err)
	}
	scope := postgres.MatchScope{Tenant: f.config.Scope.Tenant, OzonAccount: f.config.Scope.OzonAccount, MoyskladAccount: f.config.Scope.MoyskladAccount}
	if _, err := f.db.ConfirmMatch(f.ctx, scope, "1002", "product", ordersProcessID(9), "composition-fixture"); err != nil {
		t.Fatal(err)
	}
}

func (p *compositionHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v3/posting/fbs/get" {
		p.source(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && r.URL.Path != "/entity/customerorder/metadata" {
		if r.Method == http.MethodPut && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/entity/customerorder/"), "/") {
			p.replace(w, r)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/positions") {
			p.positions(w, r)
			return
		}
	}
	p.base.serve(w, r)
}

func (p *compositionHTTPProvider) source(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(403)
		return
	}
	row := p.base.base.posting("position-http-posting", "FBS")
	first := row["products"].([]any)[0].(map[string]any)
	first["quantity"], first["vat"] = p.base.quantity.Load(), "0"
	second := map[string]any{"sku": "2002", "product_id": "1002", "offer_id": "process-second", "quantity": 3, "price": "4.56", "currency_code": "RUB", "vat": "0"}
	switch p.mode.Load() {
	case 2:
		row["products"] = []any{first, second}
	case 3:
		row["products"] = []any{second}
	}
	row["updated_at"] = time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC).Add(time.Duration(p.base.revision.Load()) * time.Minute).Format(time.RFC3339)
	p.base.base.reply(w, map[string]any{"result": row})
}

func (p *compositionHTTPProvider) positions(w http.ResponseWriter, r *http.Request) {
	base := p.base.base
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, native := range base.native {
		if r.URL.Path != "/entity/customerorder/"+native.id+"/positions" || r.Header.Get("Authorization") != "Bearer synthetic-orders-moysklad-"+native.account {
			continue
		}
		rows := native.body["positions"].([]any)
		result := make([]any, 0, len(rows))
		for index, item := range rows {
			row := item.(map[string]any)
			id, known := row["id"].(string)
			if !known {
				id = ordersProcessID(999 + index)
				row["id"] = id
			}
			copy := make(map[string]any, len(row)+6)
			for key, value := range row {
				copy[key] = value
			}
			copy["accountId"], copy["shipped"], copy["discount"] = native.account, json.Number("0"), json.Number("0")
			copy["vatEnabled"] = native.body["vatEnabled"]
			copy["meta"] = base.reference("entity/customerorder/"+native.id+"/positions", "customerorderposition", id)["meta"]
			result = append(result, copy)
		}
		base.page(w, result, len(result), 100, 0)
		return
	}
	w.WriteHeader(404)
}

func (p *compositionHTTPProvider) replace(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil || len(body) != 1 {
		w.WriteHeader(400)
		return
	}
	rows, ok := body["positions"].([]any)
	if !ok || len(rows) == 0 {
		w.WriteHeader(400)
		return
	}
	base := p.base.base
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, native := range base.native {
		if r.URL.Path != "/entity/customerorder/"+native.id || r.Header.Get("Authorization") != "Bearer synthetic-orders-moysklad-"+native.account {
			continue
		}
		for index, item := range rows {
			row := item.(map[string]any)
			if _, found := row["id"]; !found {
				row["id"] = ordersProcessID(2000 + int(p.puts.Load())*10 + index)
			}
		}
		native.body["positions"] = rows
		p.puts.Add(1)
		if p.lost.Swap(false) {
			p.base.dropResponse(w)
			return
		}
		base.reply(w, base.identity(native))
		return
	}
	w.WriteHeader(404)
}

func (f compositionHTTPFixture) mode(value int32) {
	f.composition.mode.Store(value)
	f.provider.change(2, 1234)
}

func (f compositionHTTPFixture) previewComposition(t *testing.T) orders.Preview {
	t.Helper()
	f.preview(t) // Normal provider read and durable source ingestion, no writes.
	body := map[string]any{"tenant": f.config.Scope.Tenant, "ozon_account": f.config.Scope.OzonAccount, "moysklad_account": f.config.Scope.MoyskladAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{"position-http-posting"}, "actions": []orders.Action{orders.ActionMirrorCompositionUpdate}, "composition_proposals": []orders.CompositionProposal{{PostingNumber: "position-http-posting", ConfirmReplacement: true}}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	status, data := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(raw), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(data, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("normal composition HTTP preview failed: status=%d", status)
	}
	return preview
}

func (f compositionHTTPFixture) create(t *testing.T) {
	t.Helper()
	preview := f.preview(t)
	if !preview.Decisions[0].Ready {
		t.Fatalf("baseline creation blocked: %+v", preview.Decisions[0].Validation)
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
}

func TestOrdersCompositionHTTPAddRemoveRetainedUUIDReadbackAndNoop(t *testing.T) {
	f := newCompositionHTTPFixture(t)
	f.create(t)
	first := f.readNative(t).Positions[0].ID
	f.mode(2)
	preview := f.previewComposition(t)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("addition blocked: %+v", preview.Decisions[0].Validation)
	}
	replacement := preview.Decisions[0].Commands[0].PositionReplacement
	if replacement == nil || len(replacement.Added) != 1 || len(replacement.Retained) != 1 || replacement.Retained[0].NativePositionID != first {
		t.Fatal("review lost exact prior UUID")
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	target := f.readNative(t)
	if len(target.Positions) != 2 || target.Positions[0].ID != first || target.Positions[1].ID == first {
		t.Fatal("readback did not preserve native identities")
	}
	second := target.Positions[1].ID
	f.mode(3)
	preview = f.previewComposition(t)
	if !preview.Decisions[0].Ready {
		t.Fatalf("removal blocked: %+v", preview.Decisions[0].Validation)
	}
	f.assertRemovedPoolRetained(t)
	f.approve(t, preview)
	f.assertRemovedPoolRetained(t)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	target = f.readNative(t)
	if len(target.Positions) != 1 || target.Positions[0].ID != second {
		t.Fatal("remaining line was recreated or wrong line removed")
	}
	noop := f.preview(t)
	if !noop.Decisions[0].Ready || len(noop.Decisions[0].Commands) != 0 || f.composition.puts.Load() != 2 {
		t.Fatal("unchanged source created duplicate collection write")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_composition_update' AND state='RECONCILED' AND dispatch_count=1`, 2)
}

func TestOrdersCompositionHTTPRejectsForeignReserveAndSourceOrManualRace(t *testing.T) {
	cases := []struct {
		name          string
		beforePreview bool
		change        func(compositionHTTPFixture, *testing.T)
	}{
		{"foreign reserve before review", true, func(f compositionHTTPFixture, t *testing.T) { f.addForeignLine(t) }},
		{"native manual edit after approval", false, func(f compositionHTTPFixture, t *testing.T) { f.editOwned(t, "reserve", json.Number("1")) }},
		{"source changed after approval", false, func(f compositionHTTPFixture, _ *testing.T) { f.mode(3) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCompositionHTTPFixture(t)
			f.create(t)
			f.mode(2)
			if tc.beforePreview {
				tc.change(f, t)
			}
			preview := f.previewComposition(t)
			if tc.beforePreview {
				if preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
					t.Fatal("foreign inventory adopted")
				}
				return
			}
			if !preview.Decisions[0].Ready {
				t.Fatalf("valid proposal blocked: %+v", preview.Decisions[0].Validation)
			}
			f.approve(t, preview)
			tc.change(f, t)
			f.run(t, orders.StateBlocked)
			if f.composition.puts.Load() != 0 || f.latestWork(t).DispatchCount != 0 {
				t.Fatal("changed source/native state dispatched collection overwrite")
			}
		})
	}
}

func (f compositionHTTPFixture) editOwned(t *testing.T, field string, value any) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, native := range p.native {
		native.body["positions"].([]any)[0].(map[string]any)[field] = value
	}
}

func TestOrdersCompositionHTTPLostAcknowledgementNeverResendsOrAdopts(t *testing.T) {
	f := newCompositionHTTPFixture(t)
	f.create(t)
	f.mode(2)
	preview := f.previewComposition(t)
	if !preview.Decisions[0].Ready {
		t.Fatalf("review blocked: %+v", preview.Decisions[0].Validation)
	}
	f.approve(t, preview)
	f.composition.lost.Store(true)
	f.run(t, orders.StateUnknown)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateUnknown)
	if f.composition.puts.Load() != 1 {
		t.Fatal("unknown collection replacement was replayed")
	}
	review, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.PositionBaselines["position-http-posting"].Bindings) != 1 {
		t.Fatal("matching unknown readback adopted newly created native identity")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_composition_update' AND state='UNKNOWN' AND dispatch_count=1`, 1)
}

func TestOrdersCompositionHTTPRejectsTenantSubstitution(t *testing.T) {
	f := newCompositionHTTPFixture(t)
	f.create(t)
	f.mode(2)
	preview := f.previewComposition(t)
	raw, err := json.Marshal(map[string]any{"tenant": "different-merchant", "ozon_account": f.config.Scope.OzonAccount, "moysklad_account": f.config.Scope.MoyskladAccount, "preview_id": preview.ID, "acknowledge": true, "idempotency_key": "wrong-tenant-composition"})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/approve", string(raw), true)
	if status < 400 || f.composition.puts.Load() != 0 {
		t.Fatalf("tenant substitution accepted: status=%d", status)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_composition_update'`, 0)
}

// A removed source line cannot drop its old physical pool before the existing
// native reserve frontier is actually reconciled. No missing baseline is zero.
func (f compositionHTTPFixture) assertRemovedPoolRetained(t *testing.T) {
	t.Helper()
	request := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: f.config.Scope.Tenant, MoyskladAccount: f.config.Scope.MoyskladAccount, WarehouseID: ordersProcessID(4), ItemKind: "product", ItemID: ordersProcessID(8)}, Target: domain.StockTarget{OzonAccount: f.config.Scope.OzonAccount, WarehouseID: "3001", ProductID: "1001"}, Policy: domain.StockCalculationPolicy{Version: 1, Unit: "unit", SafetyStock: "0", MaxObservationAge: time.Hour, DecisionLifetime: time.Minute}}
	guard, err := f.db.OrderInventoryGuard(f.ctx, request, 0, time.Now())
	if err != nil || len(guard.Obligations) != 1 || !guard.Blocked {
		t.Fatalf("removed source pool lost pending native projection obligation: %+v %v", guard, err)
	}
}
