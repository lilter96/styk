//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type mixedRetainedFixture struct {
	f                   positionHTTPFixture
	p                   *mixedRetainedHTTPProvider
	root, child         orders.TargetObservation
	rootWork, childWork string
	staleChild          orders.Preview
}

func newMixedRetainedFixture(t *testing.T) mixedRetainedFixture {
	t.Helper()
	f, _, _ := unmirroredSplitFixture(t)
	p := &mixedRetainedHTTPProvider{base: f.provider}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	original, origin := f.readNative(t), f.latestWork(t).ID
	f.addForeignLine(t)
	nestedSettlementSplit(t, f, nestedRoot, "4")
	_, _, rootWork := repeatedRetainedReduce(t, f, nestedChild, original, origin)
	root := f.readNative(t)
	deepOwnedMirror(t, f, nestedChild)
	child, childWork := f.readNative(t), f.latestWork(t).ID
	nestedSettlementSplit(t, f, nestedChild, "2")
	stale := splitMemberHTTPPreview(t, f, nestedChild, nil)
	nestedSettlementSplit(t, f, nestedRoot, "2")
	_ = unmirroredUnassembledHTTPReview(t, f, nestedRoot)
	return mixedRetainedFixture{f: f, p: p, root: root, child: child, rootWork: rootWork, childWork: childWork, staleChild: stale}
}

func mixedRetainedReduce(t *testing.T, m mixedRetainedFixture, number, descendant string, native orders.TargetObservation, baseline string) {
	t.Helper()
	repeatedRetainedHeld(t, m.f, descendant)
	preview := splitMemberHTTPPreview(t, m.f, number, nil)
	command := preview.Decisions[0].Commands[0]
	patch := command.PositionPatch
	if command.Action != orders.ActionMirrorPositionUpdate || patch == nil || patch.Baseline.WorkID != baseline || patch.PositionID != native.Positions[0].ID || command.Correlation.CustomerOrderID != native.Correlation.CustomerOrderID || !patch.Quantity.IsKnown() || *patch.Quantity.Value != "2" || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "2" {
		t.Fatal("mixed branch reduction did not use its actual native frontier")
	}
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, descendant)
	m.f.run(t, orders.StateReconciled)
	current := m.f.readNative(t)
	if current.Correlation.CustomerOrderID != native.Correlation.CustomerOrderID {
		t.Fatal("mixed branch reduction replaced its customerorder")
	}
	assertRepeatedRetainedPosition(t, current, native.Positions[0].ID, "2")
}

func TestOrdersMixedRetainedBranchesSettleIndependentNativeFrontiers(t *testing.T) {
	m := newMixedRetainedFixture(t)
	repeatedRetainedHeld(t, m.f, mixedRetainedGrand)
	mixedRetainedReduce(t, m, nestedRoot, mixedRetainedSibling, m.root, m.rootWork)
	repeatedRetainedHeld(t, m.f, mixedRetainedGrand)
	m.f.runner = m.f.restartedRunner(t)
	deepOwnedMirror(t, m.f, mixedRetainedSibling)
	assertMixedRetainedStaleChildReview(t, m)
	mixedRetainedReduce(t, m, nestedChild, mixedRetainedGrand, m.child, m.childWork)
	deepOwnedMirror(t, m.f, mixedRetainedGrand)
	assertMixedRetainedNativeIdentities(t, m)
	assertDeepOwnedConserved(t, m.f)
	for _, number := range []string{nestedRoot, nestedChild, mixedRetainedGrand, mixedRetainedSibling} {
		if next := splitMemberHTTPPreview(t, m.f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged mixed forest created duplicate native work")
		}
	}
	if m.p.attempts.Load() != 3 || m.p.writes.Load() != 3 || m.f.provider.patches.Load() != 3 {
		t.Fatal("mixed forest duplicated a split or native reduction")
	}
}

func TestOrdersMixedRetainedUnknownChildCannotReleaseGrandchild(t *testing.T) {
	m := newMixedRetainedFixture(t)
	mixedRetainedReduce(t, m, nestedRoot, mixedRetainedSibling, m.root, m.rootWork)
	deepOwnedMirror(t, m.f, mixedRetainedSibling)
	preview := splitMemberHTTPPreview(t, m.f, nestedChild, nil)
	if command := preview.Decisions[0].Commands[0]; command.PositionPatch == nil || command.PositionPatch.Baseline.WorkID != m.childWork || command.PositionPatch.PositionID != m.child.Positions[0].ID {
		t.Fatal("mixed child review borrowed another native frontier")
	}
	m.f.approve(t, preview)
	m.f.provider.losePatchResponse.Store(true)
	m.f.run(t, orders.StateUnknown)
	unknown := m.f.latestWork(t)
	for _, number := range []string{nestedRoot, nestedChild, mixedRetainedGrand} {
		repeatedRetainedHeld(t, m.f, number)
	}
	repeatedRetainedIndependentReady(t, m.f)
	if latest := m.f.latestWork(t); latest.ID != unknown.ID || latest.State != orders.StateUnknown || latest.DispatchCount != 1 || m.p.attempts.Load() != 3 || m.p.writes.Load() != 3 || m.f.provider.patches.Load() != 3 {
		t.Fatal("UNKNOWN mixed branch was rewritten or blindly replayed")
	}
	m.f.provider.base.mu.Lock()
	defer m.f.provider.base.mu.Unlock()
	if len(m.f.provider.base.native) != 3 {
		t.Fatal("uncertain mixed child admitted a grandchild order")
	}
}

func assertMixedRetainedNativeIdentities(t *testing.T, m mixedRetainedFixture) {
	t.Helper()
	binding, err := m.f.factory.ResolveOrder(m.f.ctx, m.f.config.Scope, "moysklad", m.f.config.Scope.MoyskladAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.TargetReader)
	if !ok {
		t.Fatal("mixed frontier native reader unavailable")
	}
	for _, original := range []orders.TargetObservation{m.root, m.child} {
		current, readErr := reader.ReadCustomerOrder(m.f.ctx, original.Correlation)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if current.Correlation != original.Correlation {
			t.Fatal("mixed forest changed a native correlation")
		}
		assertRepeatedRetainedPosition(t, current, original.Positions[0].ID, "2")
	}
}

func assertMixedRetainedStaleChildReview(t *testing.T, m mixedRetainedFixture) {
	t.Helper()
	before, patches := m.f.latestWork(t).ID, m.f.provider.patches.Load()
	body, err := json.Marshal(map[string]any{"tenant": m.f.config.Scope.Tenant, "moysklad_account": m.f.config.Scope.MoyskladAccount, "ozon_account": m.f.config.Scope.OzonAccount, "preview_id": m.staleChild.ID, "acknowledge": true, "idempotency_key": "mixed-child-old-ancestor-review"})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := m.f.merchant.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	if status != http.StatusConflict || m.f.latestWork(t).ID != before || m.f.provider.patches.Load() != patches {
		t.Fatal("old child review survived its ancestor branch change or created work")
	}
}
