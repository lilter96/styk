//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func deferredLateOriginFixture(t *testing.T) (positionHTTPFixture, *repeatedRetainedHTTPProvider) {
	t.Helper()
	f, _, _ := unmirroredSplitFixture(t)
	p := &repeatedRetainedHTTPProvider{base: f.provider}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	deferredRetainedSplits(t, f, "4", "2")
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 0 || f.provider.patches.Load() != 0 {
		t.Fatal("late origin fixture fabricated an earlier native reserve")
	}
	return f, p
}

func deferredLateOriginPreview(t *testing.T, f positionHTTPFixture) orders.Preview {
	t.Helper()
	preview := splitMemberHTTPPreview(t, f, nestedRoot, nil)
	c := preview.Decisions[0].Commands[0]
	if c.Action != orders.ActionMirrorCreate || c.CustomerOrder == nil || len(c.CustomerOrder.Positions) != 1 || c.CustomerOrder.Positions[0].Quantity != "2" || !c.CustomerOrder.Positions[0].Reserve.IsKnown() || *c.CustomerOrder.Positions[0].Reserve.Value != "2" {
		t.Fatal("late origin did not reserve only the independently read latest allocation")
	}
	return preview
}

func TestOrdersDeferredRetainedLateOriginAdmitsOlderChildrenWithoutFakeReduction(t *testing.T) {
	f, p := deferredLateOriginFixture(t)
	f.approve(t, deferredLateOriginPreview(t, f))
	f.run(t, orders.StateAcknowledged)
	children := []string{nestedChild, repeatedRetainedSecond}
	deferredRetainedChildrenHeld(t, f, children)
	f.run(t, orders.StateReconciled)
	origin, root := f.latestWork(t), f.readNative(t)
	assertRepeatedRetainedPosition(t, root, root.Positions[0].ID, "2")
	for _, number := range children {
		deferredLateOriginAdmit(t, f, number, origin)
	}
	assertDeferredLateOriginConserved(t, f)
	for _, number := range append([]string{nestedRoot}, children...) {
		next := splitMemberHTTPPreview(t, f, number, nil).Decisions[0]
		if !next.Ready || len(next.Commands) != 0 {
			t.Fatal("reconciled late-origin allocation was not a stable no-op")
		}
	}
	if p.attempts.Load() != 2 || p.writes.Load() != 2 || f.provider.patches.Load() != 0 {
		t.Fatal("late initial settlement fabricated an intermediate native reduction")
	}
}

func deferredLateOriginAdmit(t *testing.T, f positionHTTPFixture, number string, origin orders.Work) {
	t.Helper()
	preview := splitMemberHTTPPreview(t, f, number, nil)
	proof := preview.Decisions[0].Commands[0].SplitAncestorSettlement
	if proof == nil || proof.Mode != orders.SplitAncestorAdmit || proof.NodeOriginWorkID != origin.ID || proof.NodeReductionWorkID != origin.ID || proof.Baseline.WorkID != origin.ID || len(proof.Baseline.Bindings) != 1 || proof.Baseline.Bindings[0].Source.Quantity != "2" {
		t.Fatal("older child did not use the real late native origin as its settled frontier")
	}
	f.approve(t, preview)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
}

func assertDeferredLateOriginConserved(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.native) != 3 {
		t.Fatal("late origin recreated or omitted a native document")
	}
	var total int64
	for _, document := range p.native {
		for _, item := range document.body["positions"].([]any) {
			line := item.(map[string]any)
			quantity, ok := line["quantity"].(json.Number)
			if !ok || line["reserve"] != quantity {
				t.Fatal("late native allocation lost exact quantity/reserve values")
			}
			n, err := quantity.Int64()
			if err != nil {
				t.Fatal(err)
			}
			total += n
		}
	}
	if total != 8 {
		t.Fatal("late native creation double exposed the pre-split physical pool")
	}
}

func TestOrdersDeferredRetainedLateUnknownOriginNeverAdmitsChildren(t *testing.T) {
	f, p := deferredLateOriginFixture(t)
	f.provider.base.mu.Lock()
	f.provider.base.lost[nestedRoot] = true
	f.provider.base.mu.Unlock()
	f.approve(t, deferredLateOriginPreview(t, f))
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	deferredRetainedChildrenHeld(t, f, []string{nestedRoot, nestedChild, repeatedRetainedSecond})
	repeatedRetainedIndependentReady(t, f)
	if latest := f.latestWork(t); latest.ID != unknown.ID || latest.State != orders.StateUnknown || latest.DispatchCount != 1 || p.writes.Load() != 2 || f.provider.patches.Load() != 0 {
		t.Fatal("unknown late creation was replayed, adopted or rewritten")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 1 {
		t.Fatal("uncertain late native origin allowed a related child creation")
	}
}
