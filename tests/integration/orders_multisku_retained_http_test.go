//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type multiskuRetainedFixture struct {
	f        positionHTTPFixture
	p        *multiskuRetainedHTTPProvider
	original orders.TargetObservation
	foreign  orders.TargetPosition
	first    orders.TargetPosition
	removed  orders.TargetPosition
}

func newMultiskuRetainedFixture(t *testing.T) multiskuRetainedFixture {
	t.Helper()
	return newMultiskuRetainedMode(t, false)
}

func newMultiskuRetainedMode(t *testing.T, retainBoth bool) multiskuRetainedFixture {
	t.Helper()
	f, _, _ := unmirroredSplitFixture(t)
	f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: "constant.false", Target: "vat_enabled"}, orders.FieldMapping{Source: "constant.true", Target: "vat_included"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "multisku-retained-vat-policy"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	compositionSecondMapping(t, f)
	p := &multiskuRetainedHTTPProvider{composition: &compositionHTTPProvider{base: f.provider}, retainBoth: retainBoth}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	original := f.readNative(t)
	if len(original.Positions) != 2 {
		t.Fatal("multi-SKU native origin did not preserve both positions")
	}
	m := multiskuRetainedFixture{f: f, p: p, original: original}
	m.first = multiskuPosition(t, original, ordersProcessID(8))
	m.removed = multiskuPosition(t, original, ordersProcessID(9))
	first, second := orders.Decimal("2"), orders.Decimal("3")
	if retainBoth {
		first, second = "4", "6"
	}
	assertRepeatedRetainedPosition(t, original, m.first.ID, first)
	assertRepeatedRetainedPosition(t, original, m.removed.ID, second)
	m.foreign = m.appendForeign(t)
	m.splitPosting(t)
	return m
}

func multiskuPosition(t *testing.T, target orders.TargetObservation, product string) orders.TargetPosition {
	t.Helper()
	var found []orders.TargetPosition
	for _, position := range target.Positions {
		if position.MoyskladID == product {
			found = append(found, position)
		}
	}
	if len(found) != 1 || found[0].ID == "" {
		t.Fatal("missing unique observed multi-SKU native position")
	}
	return found[0]
}

func (m multiskuRetainedFixture) appendForeign(t *testing.T) orders.TargetPosition {
	t.Helper()
	base := m.f.provider.base
	base.mu.Lock()
	for _, native := range base.native {
		rows := native.body["positions"].([]any)
		for _, item := range rows {
			row := item.(map[string]any)
			if row["id"] != m.removed.ID {
				continue
			}
			foreign := make(map[string]any, len(row))
			for key, value := range row {
				foreign[key] = value
			}
			foreign["id"], foreign["quantity"], foreign["reserve"] = ordersProcessID(1099), json.Number("1"), json.Number("1")
			native.body["positions"] = append(rows, foreign)
		}
	}
	base.mu.Unlock()
	for _, position := range m.f.readNative(t).Positions {
		if position.ID == ordersProcessID(1099) && position.MoyskladID == m.removed.MoyskladID {
			return position
		}
	}
	t.Fatal("foreign same-SKU row was not independently observed after origin reconciliation")
	return orders.TargetPosition{}
}

func (m multiskuRetainedFixture) splitPosting(t *testing.T) {
	t.Helper()
	review := unmirroredUnassembledHTTPReview(t, m.f, nestedRoot)
	posting := review.Decisions[0].Posting
	if len(posting.Lines) != 2 {
		t.Fatal("split source did not contain both independently mapped SKUs")
	}
	packages := make([]orders.ActionPackage, 0, 2)
	for _, line := range posting.Lines {
		packages = append(packages, orders.ActionPackage{Lines: []orders.ActionQuantity{{Identity: line.Identity, Quantity: line.Quantity}}})
	}
	if m.p.retainBoth {
		lines := make([]orders.ActionQuantity, 0, 2)
		for _, line := range posting.Lines {
			quantity := orders.Decimal("2")
			if line.Identity.SKU == "2002" {
				quantity = "3"
			}
			lines = append(lines, orders.ActionQuantity{Identity: line.Identity, Quantity: quantity})
		}
		packages = []orders.ActionPackage{{Lines: lines}, {Lines: slices.Clone(lines)}}
	}
	proposal := orders.ActionProposal{PostingNumber: nestedRoot, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: packages}}
	m.f.approve(t, splitMemberHTTPPreview(t, m.f, nestedRoot, []orders.ActionProposal{proposal}))
	m.f.run(t, orders.StateAcknowledged)
	m.f.run(t, orders.StateReconciled)
}

func (m multiskuRetainedFixture) compositionPreview(t *testing.T) orders.Preview {
	t.Helper()
	regular := unmirroredUnassembledHTTPReview(t, m.f, nestedRoot)
	m.assertOrdinaryReview(t, regular.Decisions[0])
	repeatedRetainedHeld(t, m.f, multiskuRetainedChild)
	preview := (compositionHTTPFixture{positionHTTPFixture: m.f, composition: m.p.composition}).previewComposition(t)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("explicit multi-SKU composition blocked: %+v", preview.Decisions[0].Validation)
	}
	replacement := preview.Decisions[0].Commands[0].PositionReplacement
	if replacement == nil || len(replacement.Added) != 0 || !slices.Equal(replacement.Preserved, []string{m.foreign.ID}) {
		t.Fatal("composition consent lost exact retained, removed or foreign UUID ownership")
	}
	m.assertReplacementOwnership(t, *replacement)
	return preview
}

func (m multiskuRetainedFixture) assertOrdinaryReview(t *testing.T, decision orders.Decision) {
	t.Helper()
	if m.p.retainBoth {
		m.assertScalarAlternative(t, decision)
		return
	}
	if decision.Ready || len(decision.Commands) != 0 || !slices.ContainsFunc(decision.Validation, func(v orders.Validation) bool { return v.Code == "composition_split_replacement_required" }) {
		t.Fatalf("whole-line transfer did not require explicit composition consent: ready=%v commands=%d validation=%+v", decision.Ready, len(decision.Commands), decision.Validation)
	}
}

func (m multiskuRetainedFixture) assertScalarAlternative(t *testing.T, decision orders.Decision) {
	t.Helper()
	if !decision.Ready || len(decision.Commands) != 1 {
		t.Fatalf("same-set owned reduction has no safe scalar preview: ready=%v commands=%d validation=%+v", decision.Ready, len(decision.Commands), decision.Validation)
	}
	command := decision.Commands[0]
	patch := command.PositionPatch
	if command.Action != orders.ActionMirrorPositionUpdate || patch == nil || patch.Validate(command) != nil || patch.Baseline.Correlation != m.original.Correlation || len(patch.Baseline.Bindings) != 2 || patch.PositionID != m.first.ID && patch.PositionID != m.removed.ID {
		t.Fatal("ordinary same-set preview did not freeze one exact owned native UUID")
	}
	quantity := orders.Decimal("2")
	if patch.PositionID == m.removed.ID {
		quantity = "3"
	}
	if !patch.Quantity.IsKnown() || *patch.Quantity.Value != quantity || !patch.Reserve.IsKnown() || *patch.Reserve.Value != quantity || m.p.composition.puts.Load() != 0 || m.f.provider.patches.Load() != 0 {
		t.Fatal("ordinary same-set preview dispatched a mutation or changed an unowned line")
	}
}

func (m multiskuRetainedFixture) assertReplacementOwnership(t *testing.T, replacement orders.PositionReplacement) {
	t.Helper()
	if m.p.retainBoth {
		if len(replacement.Retained) != 2 || len(replacement.Removed) != 0 || !slices.ContainsFunc(replacement.Retained, func(p orders.PositionIdentityRef) bool { return p.NativePositionID == m.first.ID }) || !slices.ContainsFunc(replacement.Retained, func(p orders.PositionIdentityRef) bool { return p.NativePositionID == m.removed.ID }) {
			t.Fatal("retained-only composition did not freeze both owned native UUIDs")
		}
		return
	}
	if len(replacement.Retained) != 1 || replacement.Retained[0].NativePositionID != m.first.ID || len(replacement.Removed) != 1 || replacement.Removed[0].NativePositionID != m.removed.ID {
		t.Fatal("full transfer did not preserve retained A and remove only owned B")
	}
}

func TestOrdersMultiskuRetainedFullTransferPreservesForeignSameSKU(t *testing.T) {
	m := newMultiskuRetainedFixture(t)
	m.f.approve(t, m.compositionPreview(t))
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, multiskuRetainedChild)
	m.f.run(t, orders.StateReconciled)
	m.assertRoot(t)
	m.f.runner = m.f.restartedRunner(t)
	deepOwnedMirror(t, m.f, multiskuRetainedChild)
	child := m.f.readNative(t)
	if child.Correlation.CustomerOrderID == m.original.Correlation.CustomerOrderID || len(child.Positions) != 1 || child.Positions[0].ID == m.removed.ID || child.Positions[0].MoyskladID != m.removed.MoyskladID || child.Positions[0].Quantity != "3" || !child.Positions[0].Reserve.IsKnown() || *child.Positions[0].Reserve.Value != "3" {
		t.Fatal("transferred B allocation did not obtain a separate exact native reserve after root readback")
	}
	m.assertRoot(t)
	for _, number := range []string{nestedRoot, multiskuRetainedChild} {
		if next := splitMemberHTTPPreview(t, m.f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("restarted multi-SKU settlement generated duplicate native work")
		}
	}
	if m.p.writes.Load() != 1 || m.p.attempts.Load() != 1 || m.p.composition.puts.Load() != 1 || m.f.provider.patches.Load() != 0 {
		t.Fatal("multi-SKU transfer repeated or issued an intermediate scalar mutation")
	}
	m.assertForeignRetainedNoop(t)
}

func (m multiskuRetainedFixture) assertForeignRetainedNoop(t *testing.T) {
	t.Helper()
	m.appendForeignRetained(t)
	preview := unmirroredUnassembledHTTPReview(t, m.f, nestedRoot)
	decision := preview.Decisions[0]
	if !decision.Ready || len(decision.Commands) != 0 || decision.Target == nil || len(decision.Target.Positions) != 3 {
		t.Fatal("foreign retained-assortment row prevented an unchanged owned-UUID no-op")
	}
	index := slices.IndexFunc(decision.Target.Positions, func(p orders.TargetPosition) bool { return p.ID == ordersProcessID(1100) })
	if index < 0 {
		t.Fatal("fresh normal preview did not observe the additional foreign UUID")
	}
	extra := decision.Target.Positions[index]
	if extra.MoyskladID != m.first.MoyskladID || extra.Quantity != "1" || !extra.Reserve.IsKnown() || *extra.Reserve.Value != "0" || m.p.composition.puts.Load() != 1 || m.f.provider.patches.Load() != 0 {
		t.Fatal("retained-assortment no-op changed foreign quantity or dispatched a write")
	}
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.native) != 2 {
		t.Fatal("foreign retained-assortment row created a duplicate customerorder")
	}
}

func (m multiskuRetainedFixture) appendForeignRetained(t *testing.T) {
	t.Helper()
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, native := range base.native {
		if native.id != m.original.Correlation.CustomerOrderID {
			continue
		}
		rows := native.body["positions"].([]any)
		for _, item := range rows {
			row := item.(map[string]any)
			if row["id"] != m.first.ID {
				continue
			}
			foreign := make(map[string]any, len(row))
			for key, value := range row {
				foreign[key] = value
			}
			foreign["id"], foreign["quantity"], foreign["reserve"] = ordersProcessID(1100), json.Number("1"), json.Number("0")
			native.body["positions"] = append(rows, foreign)
			return
		}
	}
	t.Fatal("retained owned UUID unavailable for the independent foreign-row fixture")
}

func (m multiskuRetainedFixture) assertRoot(t *testing.T) {
	t.Helper()
	root := m.readRoot(t)
	if !orders.NativePositionsEqual(root.Positions, []orders.TargetPosition{m.first, m.foreign}) {
		t.Fatal("composition changed retained A2 or foreign same-SKU B1 UUID/values")
	}
}

func (m multiskuRetainedFixture) readRoot(t *testing.T) orders.TargetObservation {
	t.Helper()
	binding, err := m.f.factory.ResolveOrder(m.f.ctx, m.f.config.Scope, "moysklad", m.f.config.Scope.MoyskladAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.TargetReader)
	if !ok {
		t.Fatal("multi-SKU native reader unavailable")
	}
	root, err := reader.ReadCustomerOrder(m.f.ctx, m.original.Correlation)
	if err != nil || !root.Complete || root.Correlation != m.original.Correlation {
		t.Fatal("composition replaced the original native document or lost complete readback", err)
	}
	return root
}

func TestOrdersMultiskuRetainedBothRowsReduceBeforeChildReserve(t *testing.T) {
	m := newMultiskuRetainedMode(t, true)
	m.f.approve(t, m.compositionPreview(t))
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, multiskuRetainedChild)
	m.f.run(t, orders.StateReconciled)
	m.assertBothRetained(t)
	m.f.runner = m.f.restartedRunner(t)
	deepOwnedMirror(t, m.f, multiskuRetainedChild)
	child := m.f.readNative(t)
	if child.Correlation.CustomerOrderID == m.original.Correlation.CustomerOrderID || len(child.Positions) != 2 {
		t.Fatal("same-set child did not obtain a separate exact native document")
	}
	for _, product := range []struct {
		id       string
		quantity orders.Decimal
	}{{ordersProcessID(8), "2"}, {ordersProcessID(9), "3"}} {
		position := multiskuPosition(t, child, product.id)
		assertRepeatedRetainedPosition(t, child, position.ID, product.quantity)
	}
	m.assertBothRetained(t)
	for _, number := range []string{nestedRoot, multiskuRetainedChild} {
		if next := splitMemberHTTPPreview(t, m.f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("same-set retained settlement repeated native work after restart")
		}
	}
	if m.p.writes.Load() != 1 || m.p.attempts.Load() != 1 || m.p.composition.puts.Load() != 1 || m.f.provider.patches.Load() != 0 {
		t.Fatal("same-set reduction replayed its split or issued scalar intermediate writes")
	}
}

func (m multiskuRetainedFixture) assertBothRetained(t *testing.T) {
	t.Helper()
	first, second := m.first, m.removed
	first.Quantity, first.Reserve = "2", orders.Present(orders.Decimal("2"))
	second.Quantity, second.Reserve = "3", orders.Present(orders.Decimal("3"))
	if !orders.NativePositionsEqual(m.readRoot(t).Positions, []orders.TargetPosition{first, second, m.foreign}) {
		t.Fatal("same-set reduction lost owned UUIDs or changed the foreign B1 reserve")
	}
}

func TestOrdersMultiskuRetainedUnknownCompositionFencesOnlyRelatedOrder(t *testing.T) {
	m := newMultiskuRetainedFixture(t)
	m.f.approve(t, m.compositionPreview(t))
	m.p.composition.lost.Store(true)
	m.f.run(t, orders.StateUnknown)
	unknown := m.f.latestWork(t)
	m.assertRoot(t) // The fixture applied the PUT but its response was lost.
	for _, number := range []string{nestedRoot, multiskuRetainedChild} {
		repeatedRetainedHeld(t, m.f, number)
	}
	m.f.runner = m.f.restartedRunner(t)
	repeatedRetainedIndependentReady(t, m.f)
	m.f.approve(t, splitMemberHTTPPreview(t, m.f, repeatedRetainedIndependent, nil))
	nestedSettlementAdvance(t, m.f, orders.StateAcknowledged)
	nestedSettlementAdvance(t, m.f, orders.StateReconciled)
	m.assertUnknownUnchanged(t, unknown)
	if m.p.composition.puts.Load() != 1 || m.p.attempts.Load() != 1 || m.p.writes.Load() != 1 {
		t.Fatal("UNKNOWN collection mutation or Seller split was blindly replayed")
	}
}

func (m multiskuRetainedFixture) assertUnknownUnchanged(t *testing.T, original orders.Work) {
	t.Helper()
	history, err := m.f.db.OrderHistory(m.f.ctx, m.f.config.Scope, orders.PageRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if work.ID == original.ID {
				if work.State != orders.StateUnknown || work.DispatchCount != 1 || !reflect.DeepEqual(work.Receipt, original.Receipt) {
					t.Fatal("lost collection outcome was falsified or replayed")
				}
				return
			}
		}
	}
	t.Fatal("original UNKNOWN collection history disappeared")
}
