//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

type catalogSyncProvider struct {
	scope   catalog.Scope
	product catalog.Product
	guard   func(context.Context) (bool, error)
	writes  int
}

func (p *catalogSyncProvider) ReadCatalog(_ context.Context, r catalog.ReadRequest) (catalog.Snapshot, error) {
	return catalog.Snapshot{Scope: p.scope, Account: p.product.Identity.Account, Products: []catalog.Product{p.product}, Complete: true, Revision: p.product.Revision, ObservedAt: time.Now().UTC()}, nil
}

func (p *catalogSyncProvider) BindCatalogIntent(_ catalog.Command, guard func(context.Context) (bool, error)) error {
	p.guard = guard
	return nil
}

func (p *catalogSyncProvider) WriteCatalog(ctx context.Context, cmd catalog.Command) (catalog.Receipt, error) {
	if p.guard == nil {
		return catalog.Receipt{}, fmt.Errorf("missing dispatch guard")
	}
	allowed, err := p.guard(ctx)
	if err != nil || !allowed {
		return catalog.Receipt{}, fmt.Errorf("dispatch guard rejected: %w", err)
	}
	p.writes++
	fields := make(map[catalog.Field]catalog.Value, len(p.product.Fields)+len(cmd.Values))
	for key, value := range p.product.Fields {
		fields[key] = value
	}
	for key, value := range cmd.Values {
		fields[key] = value
	}
	p.product.Fields = fields
	p.product.Revision += "-applied"
	p.product.ObservedAt = time.Now().UTC()
	return catalog.Receipt{CommandID: cmd.ID, State: catalog.ReceiptApplied, HTTPStatus: 200}, nil
}

func TestCatalogSyncActualEngineMixedAuthorityUsesVerifiedSiblingRebase(t *testing.T) {
	db, c, service, source, target := catalogSyncMixedFixture(t)
	resolver := service.Resolver
	p, err := service.Preview(context.Background(), catalogsync.PreviewRequest{Scope: c.Scope, ExpectedRevision: c.Revision, Actor: "catalog-test-actor"})
	if err != nil || len(p.Decisions) != 1 || len(p.Decisions[0].Commands) != 2 {
		t.Fatalf("mixed preview: %+v %v", p, err)
	}
	op, err := service.Approve(context.Background(), catalogsync.Approval{Scope: c.Scope, PreviewID: p.ID, Actor: "catalog-test-actor", IdempotencyKey: "mixed-approval"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runner := catalogsync.Runner{Enabled: true, WorkerID: "mixed-test-worker", Store: db, Resolver: resolver, AllowScope: func(scope catalog.Scope) bool { return scope == c.Scope }, Now: func() time.Time { return now }}
	for range 4 {
		worked, err := runner.RunNext(context.Background())
		if err != nil || !worked {
			t.Fatalf("mixed runner: %v %v", worked, err)
		}
		now = now.Add(6 * time.Second)
	}
	history, err := db.CatalogHistory(context.Background(), c.Scope, catalogsync.PageRequest{Limit: 10})
	if err != nil || history.Operations[0].ID != op.ID || history.Operations[0].State != catalogsync.StateReconciled || source.writes != 1 || target.writes != 1 {
		t.Fatalf("mixed reconciliation: %+v writes=%d/%d error=%v", history, source.writes, target.writes, err)
	}
	var rebases, dispatches int
	if err = db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM catalog_sync_rebases),(SELECT count(*) FROM catalog_sync_dispatch_intents)`).Scan(&rebases, &dispatches); err != nil || rebases != 1 || dispatches != 2 {
		t.Fatalf("prepared dispatch audit: %d/%d %v", rebases, dispatches, err)
	}
	again, err := service.Preview(context.Background(), catalogsync.PreviewRequest{Scope: c.Scope, ExpectedRevision: c.Revision, Actor: "catalog-test-actor"})
	if err != nil || !again.Decisions[0].Plan.Noop || len(again.Decisions[0].Commands) != 0 {
		t.Fatalf("mixed loop prevention: %+v %v", again, err)
	}
}

func catalogSyncMixedFixture(t *testing.T) (*postgres.Store, catalogsync.Config, catalogsync.Service, *catalogSyncProvider, *catalogSyncProvider) {
	t.Helper()
	db, c := catalogSyncFixture(t)
	c.Rules[catalog.FieldDescription] = catalog.OzonAuthoritative
	var err error
	c, err = db.SaveCatalogConfig(context.Background(), catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 1, Actor: "catalog-test-actor", IdempotencyKey: "mixed-authority"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := c.Targets[0]
	now := time.Now().UTC()
	source := &catalogSyncProvider{scope: c.Scope, product: catalog.Product{Identity: e.Source, Lifecycle: catalog.LifecycleActive, Revision: "source-v1", ObservedAt: now, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("New name"), catalog.FieldDescription: catalog.Text("Old description")}}}
	target := &catalogSyncProvider{scope: c.Scope, product: catalog.Product{Identity: e.Target, Lifecycle: catalog.LifecycleActive, Revision: "target-v1", ObservedAt: now, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Old name"), catalog.FieldDescription: catalog.Text("New description")}}}
	resolver := catalogsync.ResolveFunc(func(_ context.Context, scope catalog.Scope, account catalog.Account, revision int64) (catalogsync.Binding, error) {
		client := target
		if account.Provider == catalog.ProviderMoySklad {
			client = source
		}
		return catalogsync.Binding{Scope: scope, Account: account, CredentialRevision: revision, Client: client}, nil
	})
	service := catalogsync.Service{Store: db, Resolver: resolver, AllowScope: func(scope catalog.Scope) bool { return scope == c.Scope }}
	return db, c, service, source, target
}
