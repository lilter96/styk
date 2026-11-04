//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lilter96/styk/internal/identity"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

type expressExecutionHTTPProvider struct {
	base                                           *positionHTTPProvider
	writes                                         atomic.Int32
	applied, missingToken, managed, conflict, lost atomic.Bool
}

func (p *expressExecutionHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v") {
		p.base.serve(w, r)
		return
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(403)
		return
	}
	switch r.URL.Path {
	case "/v2/warehouse/list":
		p.base.base.reply(w, map[string]any{"warehouses": []any{map[string]any{"warehouse_id": json.Number("3002"), "warehouse_type": "rfbs_express", "status": "created", "is_rfbs": true, "is_express": true}}, "has_next": false})
	case "/v2/delivery-method/list":
		integration := "non_integrated"
		if p.managed.Load() {
			integration = "aggregator"
		}
		p.base.base.reply(w, map[string]any{"delivery_methods": []any{map[string]any{"id": json.Number("9007199254740993"), "warehouse_id": json.Number("3002"), "is_express": true, "status": "ACTIVE", "tpl_integration_type": integration}}, "has_next": false})
	case "/v3/posting/fbs/get":
		row := p.base.base.posting("position-http-posting", "rFBS")
		row["order_id"] = json.Number("5001")
		row["products"].([]any)[0].(map[string]any)["sku"] = json.Number("2001")
		row["products"].([]any)[0].(map[string]any)["product_id"] = json.Number("1001")
		requirements := make(map[string]any, 8)
		for _, key := range []string{"products_requiring_country", "products_requiring_change_country", "products_requiring_gtd", "products_requiring_mandatory_mark", "products_requiring_jw_uin", "products_requiring_rnpt", "products_requiring_imei", "products_requiring_weight"} {
			requirements[key] = []string{}
		}
		row["requirements"] = requirements
		row["scheme"], row["delivery_schema"], row["tpl_integration_type"] = "rFBS", "rfbs", "non_integrated"
		if p.managed.Load() && !p.conflict.Load() {
			row["tpl_integration_type"] = "aggregator"
		}
		row["delivery_method"] = map[string]any{"id": json.Number("9007199254740993"), "warehouse_id": json.Number("3002")}
		row["available_actions"] = []string{"non_int_delivering"}
		if p.missingToken.Load() {
			row["available_actions"] = []string{}
		}
		if p.applied.Load() {
			row["status"] = "delivering"
			row["updated_at"] = "2026-11-04T12:00:01Z"
		}
		p.base.base.reply(w, map[string]any{"result": row})
	case "/v2/fbs/posting/delivering":
		var body struct {
			PostingNumbers []string `json:"posting_number"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || len(body.PostingNumbers) != 1 || body.PostingNumbers[0] != "position-http-posting" {
			w.WriteHeader(400)
			return
		}
		p.writes.Add(1)
		p.applied.Store(true)
		if p.lost.Swap(false) {
			p.base.dropResponse(w)
			return
		}
		p.base.base.reply(w, map[string]any{"result": []any{map[string]any{"posting_number": "position-http-posting", "result": true, "error": ""}}})
	default:
		p.base.serve(w, r)
	}
}

func newExpressExecutionHTTPFixture(t *testing.T) (positionHTTPFixture, *expressExecutionHTTPProvider) {
	t.Helper()
	f := newExpressHTTPBase(t)
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", f.provider.base.server.URL)
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	p := &expressExecutionHTTPProvider{base: f.provider}
	p.base.base.server.Config.Handler = http.HandlerFunc(p.serve)
	now, rfbs := time.Now(), true
	market := domain.OzonImport{Account: f.config.Scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", SKU: "2001", OfferID: "process-offer"}}, Warehouses: []domain.OzonWarehouse{{ID: "3002", Name: "Synthetic independent rFBS Express", WarehouseType: "rfbs_express", Status: "created", IsRFBS: &rfbs}}}
	if _, err := f.db.SaveOzonImport(f.ctx, market, "express-independent-market"); err != nil {
		t.Fatal(err)
	}
	source := domain.InventoryImport{Account: f.config.Scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: ordersProcessID(8), Article: "process-offer"}, {Kind: "store", ID: ordersProcessID(444), Name: "Synthetic separate Express source"}}}
	if _, err := f.db.SaveInventoryImport(f.ctx, source, "express-independent-source"); err != nil {
		t.Fatal(err)
	}
	updated := f.config
	updated.StoreID = ordersProcessID(444)
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: updated, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "express-source-store"}, now)
	if err != nil {
		t.Fatal(err)
	}
	scope := postgres.MatchScope{Tenant: f.config.Scope.Tenant, MoyskladAccount: f.config.Scope.MoyskladAccount, OzonAccount: f.config.Scope.OzonAccount}
	binding, readErr := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if readErr != nil {
		t.Fatal(readErr)
	}
	initial, readErr := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "position-http-posting")
	if readErr != nil {
		t.Fatal(readErr)
	}
	countryHTTPStoreCapability(t, f.ordersStoreFixture, binding.Client, initial)
	if _, err := f.db.ConfirmWarehouseMapping(f.ctx, scope, f.config.StoreID, "3002", "synthetic-rfbs-express"); err != nil {
		t.Fatal(err)
	}
	resolver, ok := f.factory.Credentials.(*credentials.Resolver)
	if !ok {
		t.Fatal("scoped credential resolver unavailable")
	}
	protector := orderprivacy.ActionProtector{Vault: resolver.Vault, Store: f.db}
	allow := func(s orders.Scope) bool { return s == f.config.Scope }
	f.service, err = orderflow.NewService(f.db, f.factory, orderflow.ServiceOptions{AllowScope: allow, ActionProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	f.runner, err = orderflow.NewRunner(f.db, f.factory, orderflow.RunnerOptions{WorkerID: "express-http", AllowScope: allow, Service: f.service, ActionReader: protector, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: map[string]string{}}, p.base.base.server.URL, p.base.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.merchant.base = server.URL
	return f, p
}

func expressExecutionHTTPPreview(t *testing.T, f positionHTTPFixture) orders.Preview {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "position-http-posting")
	if err != nil {
		t.Fatal(err)
	}
	countryHTTPStoreCapability(t, f.ordersStoreFixture, binding.Client, posting)
	ordersStoreCommit(t, f.ordersStoreFixture, posting, fmt.Sprintf("express-%d", f.provider.ingests.Add(1)))
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{posting.PostingNumber}, "actions": []string{"posting_ship"}, "proposals": []orders.ActionProposal{{PostingNumber: posting.PostingNumber, Action: orders.ActionPostingShip, Parameters: orders.ActionParameters{EventConfirmed: true, EventReference: "SYNTHETIC-REAL-PHYSICAL-HANDOVER-EVIDENCE"}}}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("merchant Express preview HTTP%d: %s", status, raw)
	}
	return preview
}

func TestOrdersExpressHTTPAdvertisedSellerTransitionUsesNormalWorker(t *testing.T) {
	f, p := newExpressExecutionHTTPFixture(t)
	preview := expressExecutionHTTPPreview(t, f)
	if !preview.Decisions[0].Ready {
		t.Fatalf("Express blocked: %+v", preview.Decisions[0].Validation)
	}
	operation := f.approve(t, preview)
	exemplarHTTPRun(t, f.ordersStoreFixture, f.runner, operation.ID, orders.StateAcknowledged)
	exemplarHTTPRun(t, f.ordersStoreFixture, f.runner, operation.ID, orders.StateReconciled)
	if p.writes.Load() != 1 {
		t.Fatal("Express transition duplicated")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='posting_ship' AND state='RECONCILED' AND dispatch_count=1`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_action_secrets`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE work::text LIKE '%SYNTHETIC-REAL-PHYSICAL%'`, 0)
}

func TestOrdersExpressHTTPIndependentCapabilityFailuresNeverDispatch(t *testing.T) {
	for _, mode := range []string{"absent_token", "provider_managed", "conflict_after_approval"} {
		t.Run(mode, func(t *testing.T) {
			f, p := newExpressExecutionHTTPFixture(t)
			if mode == "absent_token" {
				p.missingToken.Store(true)
			}
			if mode == "provider_managed" {
				p.managed.Store(true)
			}
			preview := expressExecutionHTTPPreview(t, f)
			if mode != "conflict_after_approval" {
				if preview.Decisions[0].Ready {
					t.Fatal("unsupported Express action ready")
				}
				if p.writes.Load() != 0 {
					t.Fatal("preview dispatched")
				}
				return
			}
			if !preview.Decisions[0].Ready {
				t.Fatalf("valid baseline blocked: %+v", preview.Decisions[0].Validation)
			}
			operation := f.approve(t, preview)
			p.managed.Store(true)
			p.conflict.Store(true)
			exemplarHTTPRun(t, f.ordersStoreFixture, f.runner, operation.ID, orders.StateBlocked)
			if p.writes.Load() != 0 {
				t.Fatal("stale contradictory carrier evidence reached provider write")
			}
		})
	}
}

func newExpressHTTPBase(t *testing.T) positionHTTPFixture {
	t.Helper()
	db, vault := ordersProcessDB(t)
	session := ordersStoreOwner(t, db, "synthetic-auto-express")
	scope := ordersProcessSeed(t, db, vault, "synthetic-auto-express", 1)
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
