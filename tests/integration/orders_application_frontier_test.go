//go:build integration

package integration

import (
	"encoding/json"
	"github.com/lilter96/styk/internal/orders"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type applicationHTTPProvider struct {
	fixture   positionHTTPFixture
	puts      atomic.Int32
	cancelled atomic.Bool
	recreated atomic.Bool
}

func newApplicationHTTPFixture(t *testing.T) (positionHTTPFixture, *applicationHTTPProvider) {
	t.Helper()
	f := newPositionHTTPFixture(t)
	p := &applicationHTTPProvider{fixture: f}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "application-initial"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first := f.preview(t)
	f.approve(t, first)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	p.fixture = f
	return f, p
}

func (p *applicationHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && !strings.Contains(r.URL.Path, "/positions") {
		p.update(w, r)
		return
	}
	capture := httptest.NewRecorder()
	p.fixture.provider.serve(capture, r)
	if capture.Code != 200 {
		w.WriteHeader(capture.Code)
		return
	}
	var body map[string]any
	decoder := json.NewDecoder(capture.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		p.fixture.provider.base.t.Error(err)
		w.WriteHeader(500)
		return
	}
	if r.URL.Path == "/v3/posting/fbs/get" && p.cancelled.Load() {
		body["result"].(map[string]any)["status"] = "cancelled"
	}
	if p.recreated.Load() && strings.HasSuffix(r.URL.Path, "/positions") {
		for _, raw := range body["rows"].([]any) {
			row := raw.(map[string]any)
			row["id"] = ordersProcessID(7777)
			row["meta"] = p.fixture.provider.base.reference("entity/customerorder/"+ordersProcessID(100)+"/positions", "customerorderposition", ordersProcessID(7777))["meta"]
		}
	}
	p.fixture.provider.base.reply(w, body)
}

func (p *applicationHTTPProvider) update(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil || len(body) == 0 {
		w.WriteHeader(400)
		return
	}
	base := p.fixture.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
	for _, native := range base.native {
		if native.account != account || r.URL.Path != "/entity/customerorder/"+native.id {
			continue
		}
		for key, value := range body {
			if key != "applicable" && key != "name" {
				base.t.Errorf("unexpected scalar field %s", key)
				w.WriteHeader(400)
				return
			}
			native.body[key] = value
		}
		p.puts.Add(1)
		base.reply(w, base.document(native))
		return
	}
	w.WriteHeader(404)
}

func applicationPolicy(t *testing.T, f positionHTTPFixture, source string) positionHTTPFixture {
	t.Helper()
	for index, mapping := range f.config.FieldMappings {
		if mapping.Target == "applicable" {
			f.config.FieldMappings[index].Source = source
		}
	}
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "application-policy-" + source}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestApplicationHTTPForeignReserveCannotBeReleasedByUnposting(t *testing.T) {
	for _, source := range []string{"constant.false", "posting.cancelled"} {
		t.Run(source, func(t *testing.T) {
			f, p := newApplicationHTTPFixture(t)
			f.addForeignLine(t)
			f = applicationPolicy(t, f, source)
			p.fixture = f
			if source == "posting.cancelled" {
				p.cancelled.Store(true)
				f.provider.change(2, 1234)
			}
			preview := f.preview(t)
			if preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
				t.Fatal("foreign aggregate reserve exposed to unposting")
			}
			found := false
			for _, issue := range preview.Decisions[0].Validation {
				found = found || issue.Code == "application_frontier_ownership_unverified"
			}
			if !found {
				t.Fatalf("unexpected blocker %+v", preview.Decisions[0].Validation)
			}
			if p.puts.Load() != 0 {
				t.Fatal("unsafe application update dispatched")
			}
			target := f.readNative(t)
			if !target.Fields.Applicable.IsKnown() || !*target.Fields.Applicable.Value || len(target.Positions) != 2 || !target.Positions[1].Reserve.IsKnown() || *target.Positions[1].Reserve.Value != "2" {
				t.Fatal("foreign reserve or posting flag changed")
			}
		})
	}
}

func TestApplicationHTTPFullyOwnedFrontierCanChangePostingFlag(t *testing.T) {
	f, p := newApplicationHTTPFixture(t)
	f = applicationPolicy(t, f, "constant.false")
	p.fixture = f
	preview := f.preview(t)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 || preview.Decisions[0].Commands[0].PositionBaseline == nil {
		t.Fatalf("owned application update unavailable %+v", preview.Decisions[0].Validation)
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	if p.puts.Load() != 1 {
		t.Fatal("application intent duplicated")
	}
	target := f.readNative(t)
	if !target.Fields.Applicable.IsKnown() || *target.Fields.Applicable.Value {
		t.Fatal("posting flag readback failed")
	}
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{"position-http-posting"}, false)
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := review.PositionBaselines["position-http-posting"]
	if !found || baseline.WorkID != preview.Decisions[0].Commands[0].PositionBaseline.WorkID {
		t.Fatal("scalar flag update became a new inventory identity origin")
	}
}

func TestApplicationHTTPScalarMetadataCannotAdoptRecreatedNativeUUID(t *testing.T) {
	f, p := newApplicationHTTPFixture(t)
	base := f.provider.base
	base.mu.Lock()
	for _, native := range base.native {
		native.body["name"] = "manual-name"
	}
	base.mu.Unlock()
	p.recreated.Store(true)
	preview := f.preview(t)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 || preview.Decisions[0].Commands[0].PositionBaseline != nil {
		t.Fatalf("metadata update adopted a manual UUID %+v", preview.Decisions[0].Validation)
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{"position-http-posting"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := review.PositionBaselines["position-http-posting"]; found {
		t.Fatal("scalar reconciliation fabricated native position ownership")
	}
	f = applicationPolicy(t, f, "constant.false")
	p.fixture = f
	if next := f.preview(t); next.Decisions[0].Ready {
		t.Fatal("adopted manual UUID authorized aggregate unposting")
	}
	if p.puts.Load() != 1 {
		t.Fatal("blocked unposting dispatched")
	}
}

func TestApplicationHTTPNewForeignLineAfterReviewPreventsDispatch(t *testing.T) {
	f, p := newApplicationHTTPFixture(t)
	f = applicationPolicy(t, f, "constant.false")
	p.fixture = f
	preview := f.preview(t)
	f.approve(t, preview)
	f.addForeignLine(t)
	f.run(t, orders.StateBlocked)
	if p.puts.Load() != 0 {
		t.Fatal("stale ownership frontier dispatched")
	}
}

func TestApplicationHTTPMetadataPreservesForeignLinesWithoutAdoptingThem(t *testing.T) {
	f, p := newApplicationHTTPFixture(t)
	f.addForeignLine(t)
	base := f.provider.base
	base.mu.Lock()
	for _, native := range base.native {
		native.body["name"] = "manual-name"
	}
	base.mu.Unlock()
	preview := f.preview(t)
	if !preview.Decisions[0].Ready || preview.Decisions[0].Commands[0].PositionBaseline == nil || len(preview.Decisions[0].Commands[0].PositionBaseline.Bindings) != 1 {
		t.Fatal("scalar could not carry unchanged original ownership")
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f = applicationPolicy(t, f, "constant.false")
	p.fixture = f
	if next := f.preview(t); next.Decisions[0].Ready {
		t.Fatal("metadata reconciliation adopted a foreign line")
	}
	if p.puts.Load() != 1 {
		t.Fatal("foreign aggregate flag changed")
	}
	target := f.readNative(t)
	if len(target.Positions) != 2 || *target.Positions[1].Reserve.Value != "2" || !*target.Fields.Applicable.Value {
		t.Fatal("unmanaged reserve changed")
	}
}

func TestApplicationHTTPLegacyScalarCarriesEarlierActualCreationWithoutRewritingHistory(t *testing.T) {
	f, _ := newApplicationHTTPFixture(t)
	base := f.provider.base
	base.mu.Lock()
	for _, native := range base.native {
		native.body["name"] = "manual-name"
	}
	base.mu.Unlock()
	preview := f.preview(t)
	original := preview.Decisions[0].Commands[0].PositionBaseline
	if original == nil {
		t.Fatal("initial creator ownership unavailable")
	}
	// Seed a new immutable legacy-shaped scalar intent in the disposable DB;
	// never update an already persisted preview, command, receipt or audit row.
	preview.ID = strings.Repeat("a", 32)
	preview.Decisions[0].Commands[0].PositionBaseline = nil
	var err error
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{"position-http-posting"}, false)
	if err != nil {
		t.Fatal(err)
	}
	carried, found := review.PositionBaselines["position-http-posting"]
	if !found || !carried.Equal(*original) {
		t.Fatal("legacy scalar did not retain earlier actual UUID proof")
	}
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if history.Operations[0].Targets[0].Command.PositionBaseline != nil {
		t.Fatal("historical legacy command was rewritten")
	}
}
