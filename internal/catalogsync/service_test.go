package catalogsync

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

func TestPreviewUsesCanonicalPlanner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		change    func(*fixture)
		conflicts int
		commands  int
		noop      bool
	}{
		{name: "source changed", commands: 1},
		{name: "equal facts noop", change: equalProducts, noop: true},
		{name: "both changed conflict", change: bothChanged, conflicts: 1},
		{name: "unconfirmed mapping", change: func(f *fixture) { f.store.current.Targets[0].Mapping.Confirmed = false }},
		{name: "fenced target", change: func(f *fixture) { f.store.current.Targets[0].Fenced = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tc.change != nil {
				tc.change(f)
			}
			decision := f.preview(t).Decisions[0]
			if len(decision.Plan.Conflicts) != tc.conflicts || len(decision.Commands) != tc.commands || decision.Plan.Noop != tc.noop {
				t.Fatalf("unexpected decision: %+v", decision)
			}
			if f.source.writes+f.target.writes != 0 {
				t.Fatal("preview performed a mutation")
			}
		})
	}
}

func equalProducts(f *fixture) {
	product := f.target.products["target-1"]
	product.Fields[catalog.FieldName] = catalog.Text("new name")
	f.target.products[product.Identity.ID] = product
}

func bothChanged(f *fixture) {
	source, target := f.source.products["source-1"], f.target.products["target-1"]
	source.Fields = clone(source.Fields)
	target.Fields = clone(target.Fields)
	source.Fields[catalog.FieldName], target.Fields[catalog.FieldName] = catalog.Text("baseline name"), catalog.Text("baseline name")
	baseline, err := catalog.NewBaseline(source, target)
	if err == nil {
		f.store.current.Targets[0].Baseline = &baseline
	}
}

func TestPreviewRejectsUnavailableSelectedFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		field     catalog.Field
		authority catalog.Authority
		code      string
	}{
		{name: "unsupported price", field: catalog.FieldPrice, authority: catalog.MoySkladAuthoritative, code: "unsupported_write_field"},
		{name: "missing manual value", field: catalog.FieldName, authority: catalog.Manual, code: "manual_value_required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.store.current.Config.Rules[tc.field] = tc.authority
			decision := f.preview(t).Decisions[0]
			if decision.Ready || len(decision.Commands) != 0 || len(decision.Validation) == 0 || decision.Validation[0].Code != tc.code {
				t.Fatalf("selected field validation: %+v", decision)
			}
		})
	}
}

func TestPreviewFrozenFactsCannotBeMutatedByProvider(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	preview := f.preview(t)
	product := f.source.products["source-1"]
	product.Fields[catalog.FieldName] = catalog.Text("later change")
	f.source.products["source-1"] = product
	saved, err := f.store.CatalogPreview(f.ctx, preview.Scope, preview.ID)
	if err != nil || !catalog.EqualValue(saved.Decisions[0].Source.Fields[catalog.FieldName], catalog.Text("new name")) {
		t.Fatalf("frozen source was modified: %v", err)
	}
}

func TestApproveIsIdempotentAndRejectsStalePreview(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	preview := f.preview(t)
	approval := Approval{Scope: preview.Scope, PreviewID: preview.ID, Actor: "actor-test", IdempotencyKey: "same-approval"}
	first, err := f.service.Approve(f.ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Approve(f.ctx, approval)
	if err != nil || first.ID != second.ID || len(f.store.works) != 1 {
		t.Fatalf("idempotent approval: %v; works=%d", err, len(f.store.works))
	}
	f.store.current.Config.Revision++
	approval.IdempotencyKey = "new-approval"
	if _, err := f.service.Approve(f.ctx, approval); !errors.Is(err, ErrStale) {
		t.Fatalf("stale preview approval: %v", err)
	}
}

func TestApprovedIdempotencyReplaySurvivesExpiryAndConfigChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	preview := f.preview(t)
	approval := Approval{Scope: preview.Scope, PreviewID: preview.ID, Actor: "actor-test", IdempotencyKey: "durable-approval"}
	first, err := f.service.Approve(f.ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(10 * time.Minute)
	f.store.current.Config.Revision++
	second, err := f.service.Approve(f.ctx, approval)
	if err != nil || second.ID != first.ID || len(f.store.works) != 1 {
		t.Fatalf("approved replay must return its original operation: %v", err)
	}
}

func TestAutomaticDecisionsRequirePreviouslyAuthorizedFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		allowed    []catalog.Field
		automatic  bool
		operations int
	}{
		{name: "explicit name authorization", allowed: []catalog.Field{catalog.FieldName}, automatic: true, operations: 1},
		{name: "name was not authorized", allowed: []catalog.Field{catalog.FieldDescription}, automatic: true},
		{name: "manual enrollment", allowed: []catalog.Field{catalog.FieldName}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			enrollment := &f.store.current.Config.Targets[0]
			enrollment.Automatic, enrollment.AutomaticFields = tc.automatic, tc.allowed
			f.store.current.Targets[0].Enrollment = clone(*enrollment)
			f.store.due = true
			if _, err := f.service.Tick(f.ctx); err != nil {
				t.Fatal(err)
			}
			if len(f.store.approvals) != tc.operations || f.target.writes != 0 {
				t.Fatalf("automatic decisions=%d, writes=%d", len(f.store.approvals), f.target.writes)
			}
		})
	}
}

func TestConfigCannotAuthorizeForeignScope(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.service.AllowScope = nil
	_, err := f.service.SaveConfig(f.ctx, SaveConfigRequest{Config: f.store.current.Config, ExpectedRevision: 1, Actor: "actor-test", IdempotencyKey: "config-1"})
	if !errors.Is(err, ErrBlocked) || f.store.current.Config.Revision != 1 {
		t.Fatalf("unallowlisted enabling changed config: %v", err)
	}
}
