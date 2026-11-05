//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type lateChildFixture struct {
	f          positionHTTPFixture
	p          *lateChildHTTPProvider
	root       orders.TargetObservation
	rootWork   string
	rootSplit  string
	childSplit string
}

func newLateChildFixture(t *testing.T) lateChildFixture {
	t.Helper()
	f, _, _ := unmirroredSplitFixture(t)
	p := &lateChildHTTPProvider{base: &deepOwnedHTTPProvider{base: f.provider}}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	preview := f.preview(t) // Actual source8 refresh, never an invented native8.
	if preview.Decisions[0].Posting.Lines[0].Quantity != "8" {
		t.Fatal("late child fixture did not observe the initial provider allocation8")
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	original, origin := f.readNative(t), f.latestWork(t).ID
	f.addForeignLine(t)
	nestedSettlementSplit(t, f, nestedRoot, "4")
	rootSplit := f.latestWork(t).ID
	_, _, released := repeatedRetainedReduce(t, f, nestedChild, original, origin)
	root := f.readNative(t)
	nestedSettlementSplit(t, f, nestedChild, "2")
	return lateChildFixture{f: f, p: p, root: root, rootWork: released, rootSplit: rootSplit, childSplit: f.latestWork(t).ID}
}

func (m lateChildFixture) preview(t *testing.T) orders.Preview {
	t.Helper()
	preview := unmirroredUnassembledHTTPReview(t, m.f, nestedChild)
	decision := preview.Decisions[0]
	if !decision.Ready || len(decision.Commands) != 1 {
		t.Fatalf("first native child admission blocked: ready=%v commands=%d validation=%+v", decision.Ready, len(decision.Commands), decision.Validation)
	}
	command := decision.Commands[0]
	proof := command.SplitAncestorSettlement
	if decision.Posting.ParentPostingNumber.Presence != orders.PresenceMissing {
		t.Fatal("local node-admission proof fabricated a provider parent")
	}
	if command.Action != orders.ActionMirrorCreate || command.Correlation.CustomerOrderID != "" || command.CustomerOrder == nil || len(command.CustomerOrder.Positions) != 1 || command.CustomerOrder.Positions[0].Quantity != "2" || !command.CustomerOrder.Positions[0].Reserve.IsKnown() || *command.CustomerOrder.Positions[0].Reserve.Value != "2" {
		t.Fatal("node admission invented a previous child4 native order or wrong reserve")
	}
	if proof == nil || proof.Mode != orders.SplitAncestorNodeAdmit || proof.SplitWorkID != m.childSplit || proof.RootSplitWorkID != m.rootSplit || proof.NodeOriginWorkID != m.rootWork || proof.NodeReductionWorkID != "" || proof.Baseline.WorkID != m.rootWork || proof.Baseline.Correlation != m.root.Correlation {
		t.Fatal("node admission lost actual parent frontier or fabricated a child baseline")
	}
	if proof.Node.PostingNumber != nestedChild || proof.Source.PostingNumber != nestedChild || len(proof.Node.Lines) != 1 || proof.Node.Lines[0].Quantity != "2" || len(proof.Source.Lines) != 1 || proof.Source.Lines[0].Quantity != "2" || proof.Before.PostingNumber != nestedChild || len(proof.Before.Lines) != 1 || proof.Before.Lines[0].Quantity != "4" || len(proof.Ancestors) != 1 {
		t.Fatal("node admission did not distinguish actual child2 source from the pre-split child4 allocation")
	}
	return preview
}

func TestOrdersLateChildAdmissionUsesActualPostSplitQuantityBeforeGrandchild(t *testing.T) {
	m := newLateChildFixture(t)
	_ = sparseAncestorPreview(t, m, "2", m.childSplit)
	m.f.approve(t, m.preview(t))
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, deepOwnedGrand)
	m.f.run(t, orders.StateReconciled)
	child, childWork := m.f.readNative(t), m.f.latestWork(t).ID
	assertRepeatedRetainedPosition(t, child, child.Positions[0].ID, "2")
	m.assertRoot(t)
	m.admitGrandchild(t, child, childWork)
	nestedSettlementSplit(t, m.f, nestedChild, "1")
	m.reduceLaterChild(t, child, childWork)
	m.f.runner = m.f.restartedRunner(t)
	deepOwnedMirror(t, m.f, lateChildNewGrand)
	m.assertRoot(t)
	m.assertNoopAndConserved(t)
}

func (m lateChildFixture) admitGrandchild(t *testing.T, child orders.TargetObservation, childWork string) {
	t.Helper()
	preview := splitMemberHTTPPreview(t, m.f, deepOwnedGrand, nil)
	proof := preview.Decisions[0].Commands[0].SplitAncestorSettlement
	if proof == nil || proof.Mode != orders.SplitAncestorAdmit || proof.SplitWorkID != m.childSplit || proof.NodeReductionWorkID != childWork || proof.Baseline.WorkID != childWork || proof.Baseline.Correlation != child.Correlation {
		t.Fatal("grandchild did not use the real child2 readback as its settled frontier")
	}
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	m.f.run(t, orders.StateReconciled)
	grandchild := m.f.readNative(t)
	if grandchild.Correlation.CustomerOrderID == child.Correlation.CustomerOrderID || len(grandchild.Positions) != 1 {
		t.Fatal("grandchild reused the newly admitted child document")
	}
	assertRepeatedRetainedPosition(t, grandchild, grandchild.Positions[0].ID, "2")
}

func (m lateChildFixture) reduceLaterChild(t *testing.T, child orders.TargetObservation, childWork string) {
	t.Helper()
	repeatedRetainedHeld(t, m.f, lateChildNewGrand)
	preview := splitMemberHTTPPreview(t, m.f, nestedChild, nil)
	command := preview.Decisions[0].Commands[0]
	patch := command.PositionPatch
	if command.Action != orders.ActionMirrorPositionUpdate || patch == nil || patch.PositionID != child.Positions[0].ID || patch.Baseline.WorkID != childWork || !patch.Quantity.IsKnown() || *patch.Quantity.Value != "1" || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "1" {
		t.Fatal("later child reduction did not preserve actual child2 native provenance")
	}
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, lateChildNewGrand)
	m.f.run(t, orders.StateReconciled)
	current := m.f.readNative(t)
	if current.Correlation != child.Correlation {
		t.Fatal("later child split replaced its already admitted native document")
	}
	assertRepeatedRetainedPosition(t, current, child.Positions[0].ID, "1")
}

func (m lateChildFixture) assertRoot(t *testing.T) {
	t.Helper()
	binding, err := m.f.factory.ResolveOrder(m.f.ctx, m.f.config.Scope, "moysklad", m.f.config.Scope.MoyskladAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.TargetReader)
	if !ok {
		t.Fatal("native parent reader unavailable")
	}
	current, err := reader.ReadCustomerOrder(m.f.ctx, m.root.Correlation)
	if err != nil || !current.Complete || current.Correlation != m.root.Correlation || !orders.NativePositionsEqual(current.Positions, m.root.Positions) {
		t.Fatal("late child changed parent4 UUID or foreign reserve2", err)
	}
}

func (m lateChildFixture) assertNoopAndConserved(t *testing.T) {
	t.Helper()
	for _, number := range []string{nestedRoot, nestedChild, deepOwnedGrand, lateChildNewGrand} {
		if preview := splitMemberHTTPPreview(t, m.f, number, nil); len(preview.Decisions[0].Commands) != 0 {
			t.Fatal("late node admission or later reduction was not a stable no-op")
		}
	}
	if m.p.base.attempts.Load() != 2 || m.p.base.writes.Load() != 2 || m.p.attempts.Load() != 1 || m.p.writes.Load() != 1 || m.f.provider.patches.Load() != 2 {
		t.Fatal("late node workflow repeated a split or native reduction")
	}
	m.assertConserved(t)
}

func (m lateChildFixture) assertConserved(t *testing.T) {
	t.Helper()
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.native) != 4 {
		t.Fatal("late node created the old child4 or duplicate native order")
	}
	var reserve int64
	for _, native := range base.native {
		for _, item := range native.body["positions"].([]any) {
			value, ok := item.(map[string]any)["reserve"].(json.Number)
			if !ok {
				t.Fatal("unknown final native reserve")
			}
			n, err := value.Int64()
			if err != nil {
				t.Fatal(err)
			}
			reserve += n
		}
	}
	if reserve != 10 {
		t.Fatal("late node did not conserve owned8 plus foreign2 reserve")
	}
}

func TestOrdersLateChildAdmissionUnknownCreationFencesRelatedGraphOnly(t *testing.T) {
	m := newLateChildFixture(t)
	m.f.provider.base.mu.Lock()
	m.f.provider.base.lost[nestedChild] = true
	m.f.provider.base.mu.Unlock()
	m.f.approve(t, m.preview(t))
	m.f.run(t, orders.StateUnknown)
	unknown := m.f.latestWork(t)
	for _, number := range []string{nestedRoot, nestedChild, deepOwnedGrand} {
		repeatedRetainedHeld(t, m.f, number)
	}
	m.f.runner = m.f.restartedRunner(t)
	m.independent(t)
	m.assertUnknown(t, unknown)
	m.assertRoot(t)
}

func (m lateChildFixture) independent(t *testing.T) {
	t.Helper()
	binding, err := m.f.factory.ResolveOrder(m.f.ctx, m.f.config.Scope, "ozon", m.f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	posting, err := binding.Client.ReadOrderPosting(m.f.ctx, m.f.config.Scope, deepOwnedIndependent)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, m.f.ordersStoreFixture, posting, "late-node-independent")
	m.f.approve(t, splitMemberHTTPPreview(t, m.f, deepOwnedIndependent, nil))
	nestedSettlementAdvance(t, m.f, orders.StateAcknowledged)
	nestedSettlementAdvance(t, m.f, orders.StateReconciled)
}

func (m lateChildFixture) assertUnknown(t *testing.T, unknown orders.Work) {
	t.Helper()
	history, err := m.f.db.OrderHistory(m.f.ctx, m.f.config.Scope, orders.PageRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if work.ID == unknown.ID {
				found = work.State == orders.StateUnknown && work.DispatchCount == 1 && work.CommandFingerprint == unknown.CommandFingerprint && reflect.DeepEqual(work.Receipt, unknown.Receipt)
			}
		}
	}
	if !found || m.p.base.writes.Load() != 2 || m.p.writes.Load() != 0 || m.f.provider.patches.Load() != 1 {
		t.Fatal("UNKNOWN late node was rewritten, replayed or admitted a descendant")
	}
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.native) != 3 || base.writes[nestedChild] != 1 || base.writes[deepOwnedGrand] != 0 {
		t.Fatal("uncertain node creation leaked into a related child or duplicate dispatch")
	}
}
