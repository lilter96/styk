//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
)

type partialMirrorHTTPProvider struct {
	base   *positionHTTPProvider
	writes atomic.Int32
	lost   atomic.Bool
}

func (p *partialMirrorHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/posting/fbs/cancel-reason" {
		p.base.base.reply(w, map[string]any{"result": []any{map[string]any{"posting_number": "position-http-posting", "reasons": []any{map[string]any{"id": 352, "title": "Synthetic allowed reason"}}}}})
		return
	}
	if r.URL.Path != "/v2/posting/fbs/product/cancel" {
		p.base.serve(w, r)
		return
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(403)
		return
	}
	var body struct {
		Posting string      `json:"posting_number"`
		Reason  json.Number `json:"cancel_reason_id"`
		Items   []struct {
			SKU      json.Number `json:"sku"`
			Quantity int         `json:"quantity"`
		} `json:"items"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Posting != "position-http-posting" || body.Reason != "352" || len(body.Items) != 1 || body.Items[0].SKU != "2001" || body.Items[0].Quantity != 1 {
		w.WriteHeader(400)
		return
	}
	p.writes.Add(1)
	p.base.change(1, 1234)
	if p.lost.Swap(false) {
		p.base.dropResponse(w)
		return
	}
	p.base.base.reply(w, map[string]any{"result": body.Posting})
}

func partialMirrorFixture(t *testing.T) (positionHTTPFixture, *partialMirrorHTTPProvider) {
	t.Helper()
	f := newPositionHTTPFixture(t)
	p := &partialMirrorHTTPProvider{base: f.provider}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.config.FieldMappings = slices.DeleteFunc(f.config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "positions.reserve" || m.Target == "applicable" })
	f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "partial-mirror-reserve"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	configurePartialMirrorRuntime(t, &f)
	first := f.preview(t)
	f.approve(t, first)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, "position-http-posting")
	if err != nil {
		t.Fatal(err)
	}
	countryHTTPStoreCapability(t, f.ordersStoreFixture, binding.Client, posting)
	return f, p
}

func configurePartialMirrorRuntime(t *testing.T, f *positionHTTPFixture) {
	t.Helper()
	var err error
	resolver, ok := f.factory.Credentials.(*credentials.Resolver)
	if !ok {
		t.Fatal("scoped resolver missing")
	}
	protector := orderprivacy.ActionProtector{Vault: resolver.Vault, Store: f.db}
	allow := func(s orders.Scope) bool { return s == f.config.Scope }
	f.service, err = orderflow.NewService(f.db, f.factory, orderflow.ServiceOptions{AllowScope: allow, ActionProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	f.runner, err = orderflow.NewRunner(f.db, f.factory, orderflow.RunnerOptions{WorkerID: "partial-mirror-worker", AllowScope: allow, Service: f.service, ActionReader: protector, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: map[string]string{}}, f.provider.base.server.URL, f.provider.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.merchant.base = server.URL
}

func partialMirrorPreview(t *testing.T, f positionHTTPFixture, proposals []orders.ActionProposal) orders.Preview {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{"position-http-posting"}, "proposals": proposals})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 || !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("partial mirror preview %d: %s", status, raw)
	}
	return preview
}

func TestOrdersPartialCancellationAutomaticallyFeedsOwnedReserveMirror(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "lost_response"}[lost], func(t *testing.T) {
			f, p := partialMirrorFixture(t)
			reviewed := f.preview(t).Decisions[0].Posting
			proposal := orders.ActionProposal{PostingNumber: reviewed.PostingNumber, Action: orders.ActionPostingPartialCancel, Parameters: orders.ActionParameters{ReasonID: "352", ReasonMessage: "SYNTHETIC-PRIVATE-REASON", Lines: []orders.ActionQuantity{{Identity: reviewed.Lines[0].Identity, Quantity: "1"}}}}
			before := partialMirrorPreview(t, f, []orders.ActionProposal{proposal})
			f.approve(t, before)
			p.lost.Store(lost)
			state := orders.StateAcknowledged
			if lost {
				state = orders.StateUnknown
			}
			f.run(t, state)
			f.run(t, orders.StateReconciled)
			// No manual import or direct observation insert after the provider mutation.
			next := partialMirrorPreview(t, f, nil)
			command := next.Decisions[0].Commands[0]
			if next.Decisions[0].Posting.Lines[0].Quantity != "1" || command.Action != orders.ActionMirrorPositionUpdate || command.PositionPatch == nil || !command.PositionPatch.Reserve.IsKnown() || *command.PositionPatch.Reserve.Value != "1" {
				t.Fatal("verified cancellation was not admitted to the ordinary reserve workflow")
			}
			f.approve(t, next)
			f.provider.losePatchResponse.Store(lost)
			f.run(t, state)
			f.run(t, orders.StateReconciled)
			assertReservePolicyNative(t, f, "1")
			if p.writes.Load() != 1 || f.provider.patches.Load() != 1 {
				t.Fatal("unknown external mutation replayed")
			}
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE state='RECONCILED' AND dispatch_count=1`, 3)
		})
	}
}
