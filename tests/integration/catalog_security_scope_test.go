//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func TestCatalogSecurityScopeCannotExposeConfigPreviewOrOperation(t *testing.T) {
	f := newCatalogSecurityWorkflow(t)
	op := f.approve(t)
	tests := []struct {
		name   string
		change func(*catalog.Scope)
	}{
		{name: "tenant", change: func(s *catalog.Scope) { s.Tenant = "foreign-tenant" }},
		{name: "source_account", change: func(s *catalog.Scope) { s.MoyskladAccount = "foreign-source" }},
		{name: "destination_account", change: func(s *catalog.Scope) { s.OzonAccount = "foreign-destination" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope := f.scope
			tc.change(&scope)
			if config, err := f.db.CatalogConfig(t.Context(), scope); err == nil || config.Revision != 0 {
				t.Fatal("substituted scope exposed configuration")
			}
			if preview, err := f.db.CatalogPreview(t.Context(), scope, op.PreviewID); err == nil || preview.ID != "" {
				t.Fatal("substituted scope exposed frozen preview")
			}
			approval := catalogsync.Approval{Scope: scope, PreviewID: op.PreviewID, Actor: "synthetic-security-actor", IdempotencyKey: "foreign-approval"}
			if operation, err := f.db.ApproveCatalogPreview(t.Context(), approval, f.now); err == nil || operation.ID != "" {
				t.Fatal("substituted scope approved another account's preview")
			}
			page, err := f.db.CatalogHistory(t.Context(), scope, catalogsync.PageRequest{Limit: 10})
			if err != nil || len(page.Operations) != 0 {
				t.Fatalf("substituted scope exposed history: operations=%d error=%v", len(page.Operations), err)
			}
		})
	}
}

func TestCatalogSecurityCreationCannotBypassConfirmedMapping(t *testing.T) {
	f := newCatalogSecurityWorkflow(t)
	tests := []struct {
		name    string
		reverse bool
	}{{name: "moysklad_source"}, {name: "ozon_source", reverse: true}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := catalogSecurityDuplicateCreation(f.config, tc.reverse)
			if err := config.Validate(); err != nil {
				t.Fatalf("duplicate creation fixture is invalid: %v", err)
			}
			request := catalogsync.SaveConfigRequest{Config: config, ExpectedRevision: f.config.Revision, Actor: "synthetic-security-actor", IdempotencyKey: tc.name}
			if _, err := f.service.SaveConfig(t.Context(), request); !errors.Is(err, catalogsync.ErrStale) {
				t.Fatalf("confirmed source mapping was not enforced: %v", err)
			}
			persisted, err := f.db.CatalogConfig(t.Context(), f.scope)
			if err != nil || persisted.Revision != f.config.Revision || len(persisted.Targets) != 1 || persisted.Targets[0].Create != nil {
				t.Fatalf("rejected duplicate creation changed configuration: revision=%d error=%v", persisted.Revision, err)
			}
		})
	}
}

func catalogSecurityDuplicateCreation(config catalogsync.Config, reverse bool) catalogsync.Config {
	enrollment := config.Targets[0]
	if reverse {
		enrollment.Source, enrollment.Target = enrollment.Target, enrollment.Source
	}
	enrollment.ID, enrollment.MappingID, enrollment.MappingVersion = "duplicate-create", "", 0
	enrollment.Target.ID, enrollment.Target.SKU = "", ""
	enrollment.Create = &catalogsync.Creation{ID: "10000000-0000-0000-0000-000000000003", ExternalCode: "synthetic-duplicate-create", Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Duplicate synthetic product")}}
	config.Targets = []catalogsync.Enrollment{enrollment}
	return config
}

func TestCatalogSecurityFinalGuardRejectsSubstitutionAndDoubleBegin(t *testing.T) {
	f := newCatalogSecurityWorkflow(t)
	f.approve(t)
	f.now = f.now.Add(time.Second)
	work, err := f.db.ClaimCatalogWork(t.Context(), catalogsync.ClaimRequest{WorkerID: "synthetic-security-worker", Now: f.now, LeaseUntil: f.now.Add(time.Minute)})
	if err != nil || work == nil {
		t.Fatalf("claim reviewed catalog work: %v", err)
	}
	guard := catalogsync.DispatchGuard{Work: *work, Now: f.now}
	if allowed, err := f.db.GuardCatalogDispatch(t.Context(), guard); err != nil || !allowed {
		t.Fatalf("valid reviewed work was not dispatchable: allowed=%v error=%v", allowed, err)
	}
	catalogSecurityAssertGuardSubstitutions(t, f, *work)
	guard.Begin = true
	if allowed, err := f.db.GuardCatalogDispatch(t.Context(), guard); err != nil || !allowed {
		t.Fatalf("journal exact reviewed dispatch: allowed=%v error=%v", allowed, err)
	}
	if allowed, err := f.db.GuardCatalogDispatch(t.Context(), guard); err != nil || allowed {
		t.Fatalf("same dispatch was begun twice: allowed=%v error=%v", allowed, err)
	}
	guard.Begin = false
	if allowed, err := f.db.GuardCatalogDispatch(t.Context(), guard); err != nil || !allowed {
		t.Fatalf("final provider callback could not validate journalled intent: allowed=%v error=%v", allowed, err)
	}
}

func catalogSecurityAssertGuardSubstitutions(t *testing.T, f *catalogSecurityWorkflow, original catalogsync.Work) {
	t.Helper()
	tests := []struct {
		name   string
		change func(*catalogsync.Work)
	}{
		{name: "target", change: func(w *catalogsync.Work) { w.Command.Target.ID = "foreign" }},
		{name: "source", change: func(w *catalogsync.Work) { w.Command.Source.ID = "foreign" }},
		{name: "selection", change: func(w *catalogsync.Work) {
			w.Command.Values = map[catalog.Field]catalog.Value{catalog.FieldDescription: catalog.Text("Unreviewed")}
		}},
		{name: "config", change: func(w *catalogsync.Work) { w.ConfigRevision++ }},
		{name: "credentials", change: func(w *catalogsync.Work) { w.Credentials.Ozon++ }},
		{name: "claim", change: func(w *catalogsync.Work) { w.ClaimToken = "foreign-token" }},
		{name: "tenant", change: func(w *catalogsync.Work) { w.Scope.Tenant = "foreign-tenant" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			forged := original
			tc.change(&forged)
			allowed, err := f.db.GuardCatalogDispatch(t.Context(), catalogsync.DispatchGuard{Work: forged, Now: f.now, Begin: true})
			if allowed {
				t.Fatal("substituted durable command passed the final dispatch guard")
			}
			if err != nil && !errors.Is(err, catalogsync.ErrNotFound) && !errors.Is(err, catalogsync.ErrStale) {
				t.Fatalf("guard substitution failed for an unrelated reason: %v", err)
			}
		})
	}
}
