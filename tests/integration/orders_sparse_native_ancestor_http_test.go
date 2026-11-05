//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersSparseNativeAncestorAdmitsLeafBeforeIntermediateAndReplaysHistory(t *testing.T) {
	m := newLateChildFixture(t)
	preview := sparseAncestorPreview(t, m, "2", m.childSplit)
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, nestedChild)
	m.f.run(t, orders.StateReconciled)
	leaf, admitted := m.f.readNative(t), m.f.latestWork(t)
	sparseAssertNativeMapping(t, leaf, preview.Decisions[0].Commands[0])
	m.restoreNativeName(t, deepOwnedGrand, leaf, admitted.ID)
	if noop := splitMemberHTTPPreview(t, m.f, deepOwnedGrand, nil); len(noop.Decisions[0].Commands) != 0 {
		t.Fatal("native leaf metadata was not stable while its intermediate remained native-free")
	}

	m.f.approve(t, m.preview(t))
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, deepOwnedGrand)
	m.f.run(t, orders.StateReconciled)
	child, childWork := m.f.readNative(t), m.f.latestWork(t).ID
	m.restoreNativeName(t, nestedChild, child, childWork)
	nestedSettlementSplit(t, m.f, nestedChild, "1")
	m.reduceLaterChild(t, child, childWork)
	m.f.runner = m.f.restartedRunner(t)
	deepOwnedMirror(t, m.f, lateChildNewGrand)
	m.assertRoot(t)
	m.assertNoopAndConserved(t)
	sparseAssertTargetUnchanged(t, m, leaf)
	sparseAssertDurableWork(t, m, admitted)
	sparseAssertNativeCreates(t, m)
}

func sparseAncestorPreview(t *testing.T, m lateChildFixture, quantity orders.Decimal, split string) orders.Preview {
	t.Helper()
	preview := splitMemberHTTPPreview(t, m.f, deepOwnedGrand, nil)
	decision := preview.Decisions[0]
	if len(decision.Commands) != 1 {
		t.Fatal("sparse native leaf admission did not produce one command")
	}
	c := decision.Commands[0]
	p := c.SplitAncestorSettlement
	if c.Action != orders.ActionMirrorCreate || c.Correlation.CustomerOrderID != "" || c.CustomerOrder == nil || len(c.CustomerOrder.Positions) != 1 || c.CustomerOrder.Positions[0].Quantity != quantity || !c.CustomerOrder.Positions[0].Reserve.IsKnown() || *c.CustomerOrder.Positions[0].Reserve.Value != quantity {
		t.Fatal("sparse native admission invented an intermediate order or wrong leaf reserve")
	}
	if p == nil || p.Mode != orders.SplitAncestorNodeAdmit || p.SplitWorkID != split || p.RootSplitWorkID != m.rootSplit || p.NodeOriginWorkID != m.rootWork || p.NodeReductionWorkID != "" || p.Baseline.WorkID != m.rootWork || p.Baseline.Correlation != m.root.Correlation {
		t.Fatal("sparse admission lost the actual root native inventory authority")
	}
	if c.ExpectedSourceRevision != decision.Posting.Evidence.Revision || p.Source.Evidence.Revision != decision.Posting.Evidence.Revision || p.Source.PostingNumber != deepOwnedGrand || p.Node.PostingNumber != deepOwnedGrand || p.Before.PostingNumber != deepOwnedGrand || p.Before.Lines[0].Quantity != "2" || p.Source.Lines[0].Quantity != quantity {
		t.Fatal("sparse admission rewrote source revisions or birth/retained allocation")
	}
	sparseAssertSourcePath(t, m, *p)
	if len(p.Baseline.Bindings) != 1 || !reflect.DeepEqual(c.CustomerOrder.Positions[0].Mapping, p.Baseline.Bindings[0].Source.Mapping) {
		t.Fatal("sparse admission lost exact approved product mapping reference")
	}
	return preview
}

func sparseAssertSourcePath(t *testing.T, m lateChildFixture, p orders.SplitAncestorSettlementPermission) {
	t.Helper()
	if len(p.SourceAncestors) != 2 || p.SourceAncestors[0].PostingNumber != nestedRoot || p.SourceAncestors[1].PostingNumber != nestedChild || p.SourceAncestors[0].Lines[0].Quantity != "4" || p.SourceAncestors[1].Lines[0].Quantity != "2" {
		t.Fatal("sparse admission omitted a complete actual source ancestor")
	}
	if len(p.Ancestors) != 1 || p.Ancestors[0].Posting.PostingNumber != nestedRoot || p.Ancestors[0].WorkID != m.rootWork || p.Ancestors[0].Target.Correlation != m.root.Correlation || !orders.NativePositionsEqual(p.Ancestors[0].Target.Positions, m.root.Positions) {
		t.Fatal("sparse admission fabricated a native intermediate frontier or changed root UUIDs")
	}
	if !reflect.DeepEqual(p.AncestorNumbers(), []string{nestedRoot, nestedChild}) || p.Node.ParentPostingNumber.Presence != orders.PresenceMissing {
		t.Fatal("sparse permission lost native-free fencing or fabricated provider ancestry")
	}
	for _, source := range p.SourceAncestors {
		if source.Evidence.Revision == "" || source.PII != nil || source.Lines[0].Name.IsKnown() {
			t.Fatal("sparse source snapshot lost revision provenance or retained free text")
		}
	}
}

func sparseAssertNativeMapping(t *testing.T, native orders.TargetObservation, command orders.Command) {
	t.Helper()
	position := command.CustomerOrder.Positions[0]
	if len(native.Positions) != 1 || native.Positions[0].ID == "" || native.Positions[0].MoyskladID != position.Mapping.MoyskladID || native.Positions[0].MoyskladKind != position.Mapping.MoyskladKind {
		t.Fatal("sparse native readback lost the actual UUID or approved assortment reference")
	}
	assertRepeatedRetainedPosition(t, native, native.Positions[0].ID, position.Quantity)
}

func sparseAssertTargetUnchanged(t *testing.T, m lateChildFixture, before orders.TargetObservation) {
	t.Helper()
	binding, err := m.f.factory.ResolveOrder(m.f.ctx, m.f.config.Scope, "moysklad", m.f.config.Scope.MoyskladAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.TargetReader)
	if !ok {
		t.Fatal("sparse target reader unavailable")
	}
	after, err := reader.ReadCustomerOrder(m.f.ctx, before.Correlation)
	if err != nil || !after.Complete || after.Correlation != before.Correlation || !orders.NativePositionsEqual(after.Positions, before.Positions) {
		t.Fatal("later intermediate admission changed leaf UUID or inventory frontier", err)
	}
}

func sparseAssertDurableWork(t *testing.T, m lateChildFixture, before orders.Work) {
	t.Helper()
	history, err := m.f.db.OrderHistory(m.f.ctx, m.f.config.Scope, orders.PageRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range history.Operations {
		for _, after := range operation.Targets {
			if after.ID != before.ID {
				continue
			}
			if after.State != before.State || after.DispatchCount != 1 || after.CommandFingerprint != before.CommandFingerprint || !reflect.DeepEqual(after.Command, before.Command) || !reflect.DeepEqual(after.Receipt, before.Receipt) {
				t.Fatal("historical sparse proof, source revision or receipt was rewritten")
			}
			return
		}
	}
	t.Fatal("historical sparse admission disappeared")
}

func sparseAssertNativeCreates(t *testing.T, m lateChildFixture) {
	t.Helper()
	history, err := m.f.db.OrderHistory(m.f.ctx, m.f.config.Scope, orders.PageRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	creates := make(map[string]int)
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if work.Command.Action != orders.ActionMirrorCreate {
				continue
			}
			creates[work.Command.PostingNumber]++
			if work.Command.PostingNumber == nestedChild && work.Command.CustomerOrder.Positions[0].Quantity != "2" {
				t.Fatal("sparse history invented an earlier child4 native order")
			}
		}
	}
	if len(creates) != 4 || creates[nestedRoot] != 1 || creates[nestedChild] != 1 || creates[deepOwnedGrand] != 1 || creates[lateChildNewGrand] != 1 {
		t.Fatal("sparse history duplicated a native admission")
	}
}

func TestOrdersSparseNativeAncestorUnknownOwnSplitAdmissionFencesCompletePath(t *testing.T) {
	m := newLateChildFixture(t)
	m.f.provider.base.server.Config.Handler = http.HandlerFunc(m.p.base.serve)
	nestedSettlementSplit(t, m.f, deepOwnedGrand, "1")
	split := m.f.latestWork(t).ID
	preview := sparseAncestorPreview(t, m, "1", split)
	m.f.provider.base.mu.Lock()
	m.f.provider.base.lost[deepOwnedGrand] = true
	m.f.provider.base.mu.Unlock()
	m.f.approve(t, preview)
	m.f.run(t, orders.StateUnknown)
	unknown := m.f.latestWork(t)
	for _, number := range []string{nestedRoot, nestedChild, deepOwnedGrand, deepOwnedGreat} {
		repeatedRetainedHeld(t, m.f, number)
	}
	sparseAssertIntermediateSplitHeld(t, m, preview.Decisions[0].Commands[0].SplitAncestorSettlement.SourceAncestors[1])
	m.f.runner = m.f.restartedRunner(t)
	m.independent(t)
	sparseAssertDurableWork(t, m, unknown)
	m.assertRoot(t)
	sparseAssertUnknownWrites(t, m)
}

func sparseAssertIntermediateSplitHeld(t *testing.T, m lateChildFixture, source orders.Posting) {
	t.Helper()
	line := orders.ActionQuantity{Identity: source.Lines[0].Identity, Quantity: "1"}
	proposal := orders.ActionProposal{PostingNumber: nestedChild, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{line}}, {Lines: []orders.ActionQuantity{line}}}}}
	body, err := json.Marshal(map[string]any{"tenant": m.f.config.Scope.Tenant, "moysklad_account": m.f.config.Scope.MoyskladAccount, "ozon_account": m.f.config.Scope.OzonAccount, "expected_revision": m.f.config.Revision, "posting_numbers": []string{nestedChild}, "proposals": []orders.ActionProposal{proposal}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := m.f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 || preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("UNKNOWN descendant did not fence mutation of its native-free intermediate")
	}
}

func sparseAssertUnknownWrites(t *testing.T, m lateChildFixture) {
	t.Helper()
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.native) != 3 || base.writes[deepOwnedGrand] != 1 || base.writes[nestedChild] != 0 || base.writes[deepOwnedGreat] != 0 || base.writes[deepOwnedIndependent] != 1 || m.p.base.writes.Load() != 3 || m.f.provider.patches.Load() != 1 {
		t.Fatal("UNKNOWN sparse creation replayed, invented an intermediate order or blocked independent progress")
	}
}
