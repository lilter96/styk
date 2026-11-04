//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type unmirroredSplitHTTPProvider struct {
	base   *positionHTTPProvider
	split  atomic.Bool
	writes atomic.Int32
}

func (p *unmirroredSplitHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v3/posting/fbs/get" {
		p.posting(w, r)
		return
	}
	if r.URL.Path != "/v4/posting/fbs/ship" {
		p.base.serve(w, r)
		return
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(403)
		return
	}
	var body struct {
		Posting  string `json:"posting_number"`
		Packages []struct {
			Products []struct {
				SKU      json.Number `json:"product_id"`
				Quantity int         `json:"quantity"`
			} `json:"products"`
		} `json:"packages"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Posting != "position-http-posting" || len(body.Packages) != 2 {
		w.WriteHeader(400)
		return
	}
	for _, box := range body.Packages {
		if len(box.Products) != 1 || box.Products[0].SKU != "2001" || box.Products[0].Quantity != 1 {
			w.WriteHeader(400)
			return
		}
	}
	p.writes.Add(1)
	p.split.Store(true)
	p.base.base.reply(w, map[string]any{"result": []string{body.Posting, "split-http-child"}})
}

func (p *unmirroredSplitHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Posting string `json:"posting_number"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Posting != "position-http-posting" && body.Posting != "split-http-child" {
		w.WriteHeader(400)
		return
	}
	if body.Posting == "split-http-child" && !p.split.Load() {
		w.WriteHeader(404)
		return
	}
	row := p.base.base.posting(body.Posting, "FBS")
	row["order_id"] = json.Number("5001")
	row["delivery_method"] = map[string]any{"id": json.Number("4001"), "warehouse_id": json.Number("3001")}
	product := row["products"].([]any)[0].(map[string]any)
	product["sku"], product["vat"] = json.Number("2001"), "0"
	row["status"], row["available_actions"], row["updated_at"] = "awaiting_packaging", []string{"ship"}, "2026-11-04T12:00:00Z"
	if p.split.Load() {
		product["quantity"] = 1
		row["status"], row["available_actions"], row["updated_at"] = "awaiting_deliver", []string{}, "2026-11-04T12:00:01Z"
		if body.Posting == "split-http-child" {
			row["parent_posting_number"] = "position-http-posting"
		}
	}
	requirements := make(map[string]any)
	for _, key := range []string{"products_requiring_country", "products_requiring_change_country", "products_requiring_gtd", "products_requiring_mandatory_mark", "products_requiring_jw_uin", "products_requiring_rnpt", "products_requiring_imei", "products_requiring_weight"} {
		requirements[key] = []string{}
	}
	row["requirements"] = requirements
	p.base.base.reply(w, map[string]any{"result": row})
}

func unmirroredSplitFixture(t *testing.T) (positionHTTPFixture, *unmirroredSplitHTTPProvider, orders.Posting) {
	t.Helper()
	f := newPositionHTTPFixture(t)
	p := &unmirroredSplitHTTPProvider{base: f.provider}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.config.FieldMappings = slices.DeleteFunc(f.config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "positions.reserve" || m.Target == "applicable" })
	f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "unmirrored-split-policy"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	configurePartialMirrorRuntime(t, &f)
	initial := f.preview(t).Decisions[0].Posting
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	countryHTTPStoreCapability(t, f.ordersStoreFixture, binding.Client, initial)
	return f, p, initial
}

func splitMemberHTTPPreview(t *testing.T, f positionHTTPFixture, number string, proposals []orders.ActionProposal) orders.Preview {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{number}, "proposals": proposals})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 || !preview.Decisions[0].Ready {
		t.Fatalf("split member preview %d: %s", status, raw)
	}
	return preview
}

func TestOrdersSplitWithoutPriorParentReserveMirrorsIndependentPackages(t *testing.T) {
	f, p, initial := unmirroredSplitFixture(t)
	unit := orders.ActionQuantity{Identity: initial.Lines[0].Identity, Quantity: "1"}
	proposal := orders.ActionProposal{PostingNumber: initial.PostingNumber, Action: orders.ActionPostingSplit, Parameters: orders.ActionParameters{ReadinessConfirmed: true, Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{unit}}, {Lines: []orders.ActionQuantity{unit}}}}}
	preview := splitMemberHTTPPreview(t, f, initial.PostingNumber, []orders.ActionProposal{proposal})
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	split := f.latestWork(t)
	for _, number := range []string{"split-http-child", initial.PostingNumber} {
		next := splitMemberHTTPPreview(t, f, number, nil)
		if len(next.Decisions[0].Commands) != 1 {
			t.Fatal("split package did not produce one reviewed mirror")
		}
		command := next.Decisions[0].Commands[0]
		if command.CustomerOrder.Positions[0].Quantity != "1" || !command.CustomerOrder.Positions[0].Reserve.IsKnown() || *command.CustomerOrder.Positions[0].Reserve.Value != "1" {
			t.Fatal("package quantity was not reserved exactly once")
		}
		if number == "split-http-child" && command.SplitSourceWorkID != split.ID {
			t.Fatal("local split permission was not frozen in the intent")
		}
		f.approve(t, next)
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
	}
	for _, number := range []string{"split-http-child", initial.PostingNumber} {
		next := splitMemberHTTPPreview(t, f, number, nil)
		if len(next.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged package generated another write")
		}
	}
	if p.writes.Load() != 1 {
		t.Fatal("split was replayed")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 2 {
		t.Fatal("split produced duplicate native orders or the obsolete whole order")
	}
	for _, doc := range f.provider.base.native {
		rows := doc.body["positions"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["quantity"] != json.Number("1") || rows[0].(map[string]any)["reserve"] != json.Number("1") {
			t.Fatal("whole parent and child stock both reserved")
		}
	}
}
