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
	f, _, _ := unmirroredSplitFixture(t)
	f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: "constant.false", Target: "vat_enabled"}, orders.FieldMapping{Source: "constant.true", Target: "vat_included"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "multisku-retained-vat-policy"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	compositionSecondMapping(t, f)
	p := &multiskuRetainedHTTPProvider{composition: &compositionHTTPProvider{base: f.provider}}
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
	assertRepeatedRetainedPosition(t, original, m.first.ID, "2")
	assertRepeatedRetainedPosition(t, original, m.removed.ID, "3")
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
	proposal := orders.ActionProposal{PostingNumber: nestedRoot, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: packages}}
	m.f.approve(t, splitMemberHTTPPreview(t, m.f, nestedRoot, []orders.ActionProposal{proposal}))
	m.f.run(t, orders.StateAcknowledged)
	m.f.run(t, orders.StateReconciled)
}

func (m multiskuRetainedFixture) compositionPreview(t *testing.T) orders.Preview {
	t.Helper()
	regular := unmirroredUnassembledHTTPReview(t, m.f, nestedRoot)
	if regular.Decisions[0].Ready || len(regular.Decisions[0].Commands) != 0 || !slices.ContainsFunc(regular.Decisions[0].Validation, func(v orders.Validation) bool { return v.Code == "composition_split_replacement_required" }) {
		t.Fatal("ordinary preview did not explain that full owned-line transfer needs explicit composition consent")
	}
	repeatedRetainedHeld(t, m.f, multiskuRetainedChild)
	preview := (compositionHTTPFixture{positionHTTPFixture: m.f, composition: m.p.composition}).previewComposition(t)
	if !preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("explicit multi-SKU composition blocked: %+v", preview.Decisions[0].Validation)
	}
	replacement := preview.Decisions[0].Commands[0].PositionReplacement
	if replacement == nil || len(replacement.Retained) != 1 || replacement.Retained[0].NativePositionID != m.first.ID || len(replacement.Removed) != 1 || replacement.Removed[0].NativePositionID != m.removed.ID || len(replacement.Added) != 0 || !slices.Equal(replacement.Preserved, []string{m.foreign.ID}) {
		t.Fatal("composition consent lost exact retained, removed or foreign UUID ownership")
	}
	return preview
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
}

func (m multiskuRetainedFixture) assertRoot(t *testing.T) {
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
	if err != nil || !root.Complete || root.Correlation != m.original.Correlation || len(root.Positions) != 2 {
		t.Fatal("composition replaced the original native document or lost complete readback", err)
	}
	if !orders.NativePositionsEqual(root.Positions, []orders.TargetPosition{m.first, m.foreign}) {
		t.Fatal("composition changed retained A2 or foreign same-SKU B1 UUID/values")
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
	deepOwnedMirror(t, m.f, repeatedRetainedIndependent)
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
