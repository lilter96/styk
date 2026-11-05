//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type admissionRefreshHTTPProvider struct {
	base    *unmirroredSplitHTTPProvider
	fixture positionHTTPFixture
	writes  atomic.Int32
	lost    atomic.Bool
}

func (p *admissionRefreshHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/entity/customerorder/") || strings.Contains(strings.TrimPrefix(r.URL.Path, "/entity/customerorder/"), "/") {
		retainedUnassembledHTTPProvider{p.base}.serve(w, r)
		return
	}
	var body map[string]any
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || len(body) != 1 {
		w.WriteHeader(400)
		return
	}
	description, ok := body["description"].(string)
	if !ok {
		w.WriteHeader(400)
		return
	}
	account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
	native := p.fixture.provider.base
	native.mu.Lock()
	var receipt map[string]any
	for _, doc := range native.native {
		if r.URL.Path != "/entity/customerorder/"+doc.id || doc.account != account {
			continue
		}
		doc.body["description"] = description
		receipt = native.identity(doc)
	}
	native.mu.Unlock()
	if receipt == nil {
		w.WriteHeader(404)
		return
	}
	p.writes.Add(1)
	if p.lost.Swap(false) {
		p.fixture.provider.dropResponse(w)
		return
	}
	native.reply(w, receipt)
}

func admissionRefreshFixture(t *testing.T) (positionHTTPFixture, *admissionRefreshHTTPProvider, orders.Work) {
	t.Helper()
	f, p, root := retainedAdmissionHTTPFixture(t)
	provider := &admissionRefreshHTTPProvider{base: p, fixture: f}
	f.provider.base.server.Config.Handler = http.HandlerFunc(provider.serve)
	addSplitParentDescriptionMapping(t, &f)
	return f, provider, root
}

func completeAdmissionScalar(t *testing.T, f positionHTTPFixture, root orders.Work) orders.Work {
	t.Helper()
	preview := splitMemberHTTPPreview(t, f, root.Command.PostingNumber, nil)
	if len(preview.Decisions[0].Commands) != 1 || preview.Decisions[0].Commands[0].Action != orders.ActionMirrorUpdate {
		t.Fatal("expected actual scalar root change")
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	return f.latestWork(t)
}

func TestOrdersAdmissionRefreshKeepsChildEligibleAfterScalarParentUpdates(t *testing.T) {
	f, p, root := admissionRefreshFixture(t)
	completeAdmissionScalar(t, f, root)
	f.provider.base.mu.Lock()
	for _, doc := range f.provider.base.native {
		doc.body["description"] = "merchant changed an STYK-owned display field"
	}
	f.provider.base.mu.Unlock()
	completeAdmissionScalar(t, f, root)
	last := completeReadOnlyAdmissionScalar(t, f, root)
	next := unmirroredUnassembledHTTPReview(t, f, "split-http-child")
	if !next.Decisions[0].Ready || len(next.Decisions[0].Commands) != 1 || next.Decisions[0].Commands[0].SplitTransfer == nil || next.Decisions[0].Commands[0].SplitTransfer.ParentWorkID != last.ID {
		t.Fatal("scalar history permanently blocked unchanged owned allocation")
	}
	f.approve(t, next)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertRetainedAdmissionNativeTotal(t, f, 2)
	if p.writes.Load() != 2 || p.base.writes.Load() != 1 {
		t.Fatal("scalar refresh repeated writes or split")
	}
	noop := splitMemberHTTPPreview(t, f, "split-http-child", nil)
	if len(noop.Decisions[0].Commands) != 0 {
		t.Fatal("unchanged child was not a no-op")
	}
}

func TestOrdersAdmissionRefreshDoesNotHideManualNativeFrontierChange(t *testing.T) {
	f, p, root := admissionRefreshFixture(t)
	completeAdmissionScalar(t, f, root)
	preview := splitMemberHTTPPreview(t, f, "split-http-child", nil)
	f.approve(t, preview)
	f.addForeignLine(t)
	f.run(t, orders.StateBlocked)
	if f.latestWork(t).DispatchCount != 0 || p.writes.Load() != 1 || p.base.writes.Load() != 1 {
		t.Fatal("fresh native drift was hidden by scalar history")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 1 {
		t.Fatal("stale frontier created a child")
	}
}

func TestOrdersAdmissionRefreshUnknownScalarStillFencesChild(t *testing.T) {
	f, p, root := admissionRefreshFixture(t)
	p.lost.Store(true)
	preview := splitMemberHTTPPreview(t, f, root.Command.PostingNumber, nil)
	f.approve(t, preview)
	f.run(t, orders.StateUnknown)
	child := unmirroredUnassembledHTTPReview(t, f, "split-http-child")
	if child.Decisions[0].Ready || len(child.Decisions[0].Commands) != 0 {
		t.Fatal("unknown scalar work disappeared from history witness")
	}
	if p.writes.Load() != 1 || p.base.writes.Load() != 1 {
		t.Fatal("unknown scalar or split was replayed")
	}
}

func completeReadOnlyAdmissionScalar(t *testing.T, f positionHTTPFixture, root orders.Work) orders.Work {
	t.Helper()
	f.provider.base.mu.Lock()
	for _, doc := range f.provider.base.native {
		doc.body["description"] = "manual display edit before approved check"
	}
	f.provider.base.mu.Unlock()
	preview := splitMemberHTTPPreview(t, f, root.Command.PostingNumber, nil)
	if len(preview.Decisions[0].Commands) != 1 {
		t.Fatal("missing scalar decision")
	}
	f.approve(t, preview)
	f.provider.base.mu.Lock()
	for _, doc := range f.provider.base.native {
		doc.body["description"] = "awaiting_packaging"
	}
	f.provider.base.mu.Unlock()
	f.run(t, orders.StateReconciled)
	work := f.latestWork(t)
	var intents int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_write_intents WHERE work_id=$1`, work.ID).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if work.DispatchCount != 0 || work.Receipt.HTTPStatus != 0 || work.Receipt.State != "" || intents != 0 {
		t.Fatal("read-only reconciliation invented a dispatch or acknowledgment")
	}
	return work
}
