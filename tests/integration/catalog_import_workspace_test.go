//go:build integration

package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

type catalogImportProvider struct{ catalogSyncProvider }

func (p *catalogImportProvider) LookupCatalogCreate(_ context.Context, cmd catalog.Command) (catalog.Snapshot, error) {
	return catalog.Snapshot{Scope: cmd.Scope, Account: cmd.Target.Account, Complete: true, ObservedAt: time.Now(), Revision: "absence-v1"}, nil
}
func (p *catalogImportProvider) ValidateCatalog(_ context.Context, _ catalog.Command) ([]catalog.FieldError, error) {
	return nil, nil
}

func importWorkspaceService(t *testing.T, db *postgres.Store, scope catalog.Scope) (catalogsync.Service, catalogsync.Enrollment) {
	t.Helper()
	e := catalogsync.Enrollment{Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}, EntityType: "product", ID: "101", OfferID: "A"}, Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: scope.MoyskladAccount}, EntityType: "product"}, Create: &catalogsync.Creation{Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Чашка синяя"), catalog.FieldArticle: catalog.Text("A")}}}
	resolver := catalogsync.ResolveFunc(func(_ context.Context, s catalog.Scope, a catalog.Account, r int64) (catalogsync.Binding, error) {
		p := &catalogImportProvider{catalogSyncProvider{scope: s, product: catalog.Product{Identity: e.Source, Lifecycle: catalog.LifecycleActive, Revision: "v1", ObservedAt: time.Now(), Fields: e.Create.Values}}}
		return catalogsync.Binding{Scope: s, Account: a, CredentialRevision: r, Client: p}, nil
	})
	return catalogsync.Service{Store: db, Resolver: resolver, AllowScope: func(s catalog.Scope) bool { return s == scope }}, e
}

func TestCatalogImportBatchPreservesDisabledRulesAndFencesReplay(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	service, e := importWorkspaceService(t, db, scope)
	initial, err := db.CatalogConfig(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	p, err := service.PreviewImport(ctx, catalogsync.ImportRequest{Scope: scope, Targets: []catalogsync.Enrollment{e}, Actor: "import-test"})
	if err != nil || len(p.Decisions) != 1 || !p.Decisions[0].Ready {
		t.Fatalf("import plan: %+v %v", p, err)
	}
	after, err := db.CatalogConfig(ctx, scope)
	if err != nil || after.Enabled || after.TestEnabled || !reflect.DeepEqual(after.Rules, initial.Rules) || len(after.Targets) != 0 {
		t.Fatalf("import changed rules: %+v %v", after, err)
	}
	a := catalogsync.Approval{Scope: scope, PreviewID: p.ID, Actor: "import-test", IdempotencyKey: "one-batch", EnrollmentIDs: []string{p.Decisions[0].EnrollmentID}}
	op, err := service.Approve(ctx, a)
	if err != nil || len(op.Targets) != 1 || !op.Targets[0].OneTimeImport {
		t.Fatalf("approve: %+v %v", op, err)
	}
	replay, err := service.Approve(ctx, a)
	if err != nil || replay.ID != op.ID {
		t.Fatalf("replay: %v", err)
	}
	// A lost browser cannot bypass the durable identity by inventing a new key.
	p2, err := service.PreviewImport(ctx, catalogsync.ImportRequest{Scope: scope, Targets: []catalogsync.Enrollment{e}, Actor: "import-test"})
	if err != nil || p2.Decisions[0].Ready {
		t.Fatalf("active creation not blocked: %+v %v", p2, err)
	}
	w := catalogSyncClaim(t, db)
	ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Begin: true, Now: time.Now()})
	if err != nil || !ok {
		t.Fatalf("disabled config blocked authorized one-time import: %v %v", ok, err)
	}
	a.IdempotencyKey = "another"
	if _, err = service.Approve(ctx, a); !errors.Is(err, catalogsync.ErrConflict) && !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("second operation accepted: %v", err)
	}
}

func TestCatalogImportBlockedItemDoesNotExcludeReadySubset(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	service, ready := importWorkspaceService(t, db, scope)
	absent := ready
	absent.Source.ID = "missing"
	p, err := service.PreviewImport(ctx, catalogsync.ImportRequest{Scope: scope, Targets: []catalogsync.Enrollment{ready, absent}, Actor: "import-test"})
	if err != nil || len(p.Decisions) != 2 || !p.Decisions[0].Ready || p.Decisions[1].Ready {
		t.Fatalf("mixed plan: %+v %v", p, err)
	}
	a := catalogsync.Approval{Scope: scope, PreviewID: p.ID, Actor: "import-test", IdempotencyKey: "subset", EnrollmentIDs: []string{p.Decisions[0].EnrollmentID}}
	op, err := service.Approve(ctx, a)
	if err != nil || len(op.Targets) != 1 {
		t.Fatalf("ready subset: %+v %v", op, err)
	}
}
