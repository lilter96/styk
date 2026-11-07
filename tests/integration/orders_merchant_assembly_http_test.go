//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

type merchantAssemblySource struct {
	status, updated         string
	missing, marked, denied bool
}

type merchantAssemblyHTTPFixture struct {
	*nativeLifecycleHTTPFixture
	source atomic.Pointer[merchantAssemblySource]
	writes atomic.Int32
}

func newMerchantAssemblyHTTPFixture(t *testing.T) *merchantAssemblyHTTPFixture {
	t.Helper()
	f := &merchantAssemblyHTTPFixture{nativeLifecycleHTTPFixture: newNativeLifecycleHTTPFixture(t)}
	f.source.Store(&merchantAssemblySource{status: "awaiting_packaging", updated: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	f.provider.base.server.Config.Handler = http.HandlerFunc(f.serveAssembly)
	f.config.Lifecycle = &orders.LifecyclePolicy{Enabled: true, DemandMoment: orders.OnHandoffConfirmed, Mappings: []orders.MerchantCommandMapping{{StateID: ordersProcessID(21001), FromStateIDs: []string{f.config.StateID}, Action: orders.ActionPostingPack, Scheme: "FBS", EventKind: "assembly_ready", WholePosting: true}}}
	f.saveAssemblyConfig(t, "assembly-independent-owner-consent", true)
	// The shared fixture already proves native creation. This slice starts at
	// the existing correlated order and exercises the real assembly workflow.
	f.preview(t)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	return f
}

func (f *merchantAssemblyHTTPFixture) saveAssemblyConfig(t *testing.T, key string, consent bool) {
	t.Helper()
	body := ordersHTTPBody(f.config.Scope)
	body["config"], body["expected_revision"], body["idempotency_key"], body["lifecycle_consent"] = f.config, f.config.Revision, key, consent
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	status, response := f.merchant.request(t, http.MethodPost, "/api/orders/config", string(raw), true)
	if status != http.StatusOK || json.Unmarshal(response, &f.config) != nil {
		t.Fatalf("owner lifecycle API save: HTTP %d %s", status, response)
	}
}

func (f *merchantAssemblyHTTPFixture) serveAssembly(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v4/posting/fbs/ship" {
		f.assemble(w, r)
		return
	}
	if r.URL.Path != "/v3/posting/fbs/get" {
		f.nativeLifecycleHTTPFixture.serve(w, r)
		return
	}
	capture := httptest.NewRecorder()
	f.nativeLifecycleHTTPFixture.serve(capture, r)
	var body map[string]any
	decoder := json.NewDecoder(capture.Body)
	decoder.UseNumber()
	if capture.Code != http.StatusOK || decoder.Decode(&body) != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	result := body["result"].(map[string]any)
	source := f.source.Load()
	result["status"], result["updated_at"] = source.status, source.updated
	result["available_actions"] = []string{"ship"}
	if source.denied {
		result["available_actions"] = []string{}
	}
	requirements := make(map[string]any, len(orders.PackingRequirementKinds()))
	for _, kind := range orders.PackingRequirementKinds() {
		requirements["products_requiring_"+string(kind)] = []any{}
	}
	if source.marked {
		requirements["products_requiring_mandatory_mark"] = []int64{2001}
	}
	if !source.missing {
		result["requirements"] = requirements
	}
	f.provider.base.reply(w, body)
}

func (f *merchantAssemblyHTTPFixture) assemble(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Client-Id") != f.config.Scope.OzonAccount || r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+f.config.Scope.OzonAccount {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var request struct {
		Posting  string `json:"posting_number"`
		Packages []struct {
			Products []struct {
				ID       int64 `json:"product_id"`
				Quantity int64 `json:"quantity"`
			} `json:"products"`
		} `json:"packages"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Posting != "position-http-posting" || len(request.Packages) != 1 || len(request.Packages[0].Products) != 1 || request.Packages[0].Products[0].ID != 2001 || request.Packages[0].Products[0].Quantity != 1 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.writes.Add(1)
	current := *f.source.Load()
	current.status, current.updated = "awaiting_deliver", time.Now().UTC().Format(time.RFC3339Nano)
	f.source.Store(&current)
	f.provider.base.reply(w, map[string]any{"result": []string{request.Posting}})
}

func TestOrdersMerchantAssemblyHTTPObservedTransitionUsesExistingOutboxOnce(t *testing.T) {
	f := newMerchantAssemblyHTTPFixture(t)
	f.sellerTransition(t, ordersProcessID(21001))
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents WHERE state='QUEUED'`, 1)
	f.run(t, orders.StateAcknowledged)
	if f.writes.Load() != 1 || f.latestWork(t).Command.Action != orders.ActionPostingPack {
		t.Fatal("native seller transition did not dispatch exactly one real v4 assembly request")
	}
	f.run(t, orders.StateReconciled)
	f.sellerTransition(t, ordersProcessID(21001))
	if err := f.service.ProcessMerchantIntents(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE work->>'automatic'='true' AND action='posting_pack' AND state='RECONCILED' AND dispatch_count=1`, 1)
	if f.writes.Load() != 1 || f.native.writes.Load() != 0 || f.native.shipped.Load() != 0 {
		t.Fatal("assembly or replay caused duplicate Ozon action or premature native stock movement")
	}
	f.assertPhysicalStock(t, "10")
	f.assertNative(t)
	status, raw := f.merchant.request(t, http.MethodGet, "/api/orders/merchant-intents"+ordersHTTPQuery(f.config.Scope), "", false)
	if status != http.StatusOK || !strings.Contains(string(raw), "assembly_ready") {
		t.Fatalf("merchant intent API did not expose durable event: HTTP %d", status)
	}
}

func TestOrdersMerchantAssemblyHTTPMissingPrerequisitesNeverDispatch(t *testing.T) {
	for _, kind := range []string{"requirements_missing", "marking_required", "provider_action_denied"} {
		t.Run(kind, func(t *testing.T) {
			f := newMerchantAssemblyHTTPFixture(t)
			current := *f.source.Load()
			current.missing, current.marked, current.denied = kind == "requirements_missing", kind == "marking_required", kind == "provider_action_denied"
			f.source.Store(&current)
			f.preview(t)
			f.sellerTransition(t, ordersProcessID(21001))
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents WHERE state='BLOCKED'`, 1)
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='posting_pack'`, 0)
			if f.writes.Load() != 0 || f.native.writes.Load() != 0 {
				t.Fatal("incomplete action evidence reached a provider write")
			}
		})
	}
}

func TestOrdersMerchantAssemblyHTTPPendingIntentSurvivesForwardPositionRefresh(t *testing.T) {
	f := newMerchantAssemblyHTTPFixture(t)
	f.state.Store(&nativeLifecycleHTTPState{ID: ordersProcessID(21001), Updated: time.Now().In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents WHERE state='OBSERVED'`, 1)
	f.provider.change(2, 1234)
	source := *f.source.Load()
	source.updated = time.Now().UTC().Format(time.RFC3339Nano)
	f.source.Store(&source)
	patches := f.provider.patches.Load()
	preview := f.preview(t)
	if len(preview.Decisions) != 1 || preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 || len(preview.Decisions[0].Validation) == 0 {
		t.Fatal("forward refresh can overwrite a pending exact native merchant revision")
	}
	target := f.readNative(t)
	if target.Fields.StateID != ordersProcessID(21001) || f.provider.patches.Load() != patches || f.writes.Load() != 0 {
		t.Fatal("forward preview erased seller state or dispatched a pending action")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_merchant_intents WHERE state='OBSERVED'`, 1)
}

func TestOrdersMerchantAssemblyHTTPNativeCompositionMismatchNeverDispatches(t *testing.T) {
	f := newMerchantAssemblyHTTPFixture(t)
	base := f.provider.base
	base.mu.Lock()
	for _, native := range base.native {
		rows := native.body["positions"].([]any)
		rows[0].(map[string]any)["quantity"] = json.Number("2")
	}
	base.mu.Unlock()
	f.sellerTransition(t, ordersProcessID(21001))
	items, err := f.db.OrderMerchantIntents(f.ctx, f.config.Scope, 10)
	if err != nil || len(items) != 1 || items[0].State != "BLOCKED" || items[0].Code != "merchant_packing_owned_positions_changed" {
		t.Fatal("edited native composition did not produce a specific intervention", items, err)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='posting_pack'`, 0)
	if f.writes.Load() != 0 || f.native.writes.Load() != 0 {
		t.Fatal("native composition mismatch reached a provider write")
	}
}

type merchantAssemblyAdmissionStore struct {
	*postgres.Store
	once  atomic.Bool
	after func() error
}

func (s *merchantAssemblyAdmissionStore) StartProviderCall(ctx context.Context, lease providercall.Lease) (time.Duration, error) {
	delay, err := s.Store.StartProviderCall(ctx, lease)
	sum := sha256.Sum256([]byte("v4/posting/fbs/ship"))
	if err == nil && delay <= 0 && lease.Attempt.Operation == hex.EncodeToString(sum[:]) && s.once.CompareAndSwap(false, true) {
		err = s.after()
	}
	return delay, err
}

func (f *merchantAssemblyHTTPFixture) afterAssemblyAdmission(t *testing.T, callback func() error) *merchantAssemblyAdmissionStore {
	t.Helper()
	store := &merchantAssemblyAdmissionStore{Store: f.db, after: callback}
	gate, err := providercall.New(store, providercall.Options{SyntheticInterval: 20 * time.Millisecond, MaxInflight: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.factory.Gate = gate
	resolver := f.factory.Credentials.(*credentials.Resolver)
	protector := orderprivacy.ActionProtector{Vault: resolver.Vault, Store: f.db}
	allow := func(scope orders.Scope) bool { return scope == f.config.Scope }
	f.service, err = orderflow.NewService(f.db, f.factory, orderflow.ServiceOptions{AllowScope: allow, ActionProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	f.runner, err = orderflow.NewRunner(f.db, f.factory, orderflow.RunnerOptions{WorkerID: "merchant-assembly-admission", AllowScope: allow, Service: f.service, ActionReader: protector, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestOrdersMerchantAssemblyHTTPFinalAdmissionRechecksConsentAndNativeIntent(t *testing.T) {
	for _, change := range []string{"revoke_consent", "native_state_changed"} {
		t.Run(change, func(t *testing.T) {
			f := newMerchantAssemblyHTTPFixture(t)
			f.sellerTransition(t, ordersProcessID(21001))
			hook := f.afterAssemblyAdmission(t, func() error {
				if change == "native_state_changed" {
					f.state.Store(&nativeLifecycleHTTPState{ID: ordersProcessID(21002), Updated: time.Now().In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
					return nil
				}
				config := f.config
				policy := *config.Lifecycle
				policy.Enabled = false
				config.Lifecycle = &policy
				_, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "assembly-admission-revoke"}, time.Now())
				return err
			})
			f.run(t, orders.StateBlocked)
			if !hook.once.Load() || f.writes.Load() != 0 || f.latestWork(t).DispatchCount != 0 {
				t.Fatal("final admission recheck did not prevent superseded seller command")
			}
		})
	}
}
