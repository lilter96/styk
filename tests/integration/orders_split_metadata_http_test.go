//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersLateChildAdmissionMetadataPreservesInventoryAuthority(t *testing.T) {
	m := newLateChildFixture(t)
	metadata := m.restoreNativeName(t, nestedRoot, m.root, m.rootWork)
	preview := m.preview(t)
	ancestors := preview.Decisions[0].Commands[0].SplitAncestorSettlement.Ancestors
	if ancestors[len(ancestors)-1].WorkID != metadata {
		t.Fatal("admission discarded the actual latest metadata acknowledgement")
	}
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	m.f.run(t, orders.StateReconciled)
	child, childWork := m.f.readNative(t), m.f.latestWork(t).ID
	m.restoreNativeName(t, nestedChild, child, childWork)
	m.admitGrandchild(t, child, childWork)
	nestedSettlementSplit(t, m.f, nestedChild, "1")
	m.reduceLaterChild(t, child, childWork)
	m.f.runner = m.f.restartedRunner(t)
	deepOwnedMirror(t, m.f, lateChildNewGrand)
	m.assertRoot(t)
	m.assertNoopAndConserved(t)
}

func (m lateChildFixture) restoreNativeName(t *testing.T, number string, before orders.TargetObservation, authority string) string {
	t.Helper()
	p := &applicationHTTPProvider{fixture: m.f}
	m.f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && !strings.Contains(r.URL.Path, "/positions") {
			p.update(w, r)
			return
		}
		m.p.serve(w, r)
	})
	m.changeNativeName(before.Correlation.CustomerOrderID)
	preview := splitMemberHTTPPreview(t, m.f, number, nil)
	c := preview.Decisions[0].Commands[0]
	if c.Action != orders.ActionMirrorUpdate || c.CustomerOrder == nil || len(c.CustomerOrder.OwnedFields) != 1 || c.CustomerOrder.OwnedFields[0] != "name" || c.PositionBaseline == nil || c.PositionBaseline.WorkID != authority {
		t.Fatal("metadata did not preserve the actual inventory authority")
	}
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	m.f.run(t, orders.StateReconciled)
	after := m.f.readNative(t)
	if p.puts.Load() != 1 || after.Correlation != before.Correlation || !orders.NativePositionsEqual(after.Positions, before.Positions) {
		t.Fatal("metadata changed, adopted or duplicated a native inventory position")
	}
	return m.f.latestWork(t).ID
}

func (m lateChildFixture) changeNativeName(id string) {
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, native := range base.native {
		if native.id == id {
			native.body["name"] = "merchant-edited-name"
		}
	}
}
