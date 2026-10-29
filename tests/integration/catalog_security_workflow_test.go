//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/postgres"
)

type catalogSecurityProvider struct {
	product   catalog.Product
	command   catalog.Command
	authorize func(context.Context) (bool, error)
	writes    int
	polls     int
	result    func(catalog.Command) (catalog.Receipt, error)
	poll      func(catalog.Command, catalog.Receipt) (catalog.Receipt, error)
}

func (p *catalogSecurityProvider) ReadCatalog(_ context.Context, req catalog.ReadRequest) (catalog.Snapshot, error) {
	if req.Account != p.product.Identity.Account || len(req.Identities) != 1 || !req.Identities[0].SameEntity(p.product.Identity) {
		return catalog.Snapshot{}, catalog.ErrIdentityMismatch
	}
	return catalog.Snapshot{Scope: req.Scope, Account: req.Account, Complete: true, Products: []catalog.Product{p.product}}, nil
}

func (p *catalogSecurityProvider) BindCatalogIntent(command catalog.Command, authorize func(context.Context) (bool, error)) error {
	if p.authorize != nil || authorize == nil {
		return catalog.ErrInvalidCommand
	}
	p.command, p.authorize = command, authorize
	return nil
}

func (p *catalogSecurityProvider) WriteCatalog(ctx context.Context, command catalog.Command) (catalog.Receipt, error) {
	have, err := catalog.CommandFingerprint(command)
	if err != nil {
		return catalog.Receipt{}, err
	}
	want, err := catalog.CommandFingerprint(p.command)
	if err != nil || p.authorize == nil || have != want {
		return catalog.Receipt{}, catalog.ErrInvalidCommand
	}
	allowed, err := p.authorize(ctx)
	if err != nil || !allowed {
		return catalog.Receipt{}, catalogsync.ErrBlocked
	}
	p.authorize = nil
	p.writes++
	if p.result != nil {
		return p.result(command)
	}
	return catalogSecurityAccepted(command), nil
}

func (p *catalogSecurityProvider) PollCatalog(_ context.Context, command catalog.Command, receipt catalog.Receipt) (catalog.Receipt, error) {
	p.polls++
	if p.poll != nil {
		return p.poll(command, receipt)
	}
	return receipt, nil
}

func catalogSecurityAccepted(command catalog.Command) catalog.Receipt {
	return catalog.Receipt{CommandID: command.ID, State: catalog.ReceiptAccepted, TaskID: "501",
		Products: []catalog.ProductReceipt{{ID: command.Target.ID, OfferID: command.Target.OfferID, Status: "pending"}}}
}

type catalogSecurityWorkflow struct {
	db      *postgres.Store
	scope   catalog.Scope
	config  catalogsync.Config
	service catalogsync.Service
	runner  catalogsync.Runner
	source  *catalogSecurityProvider
	target  *catalogSecurityProvider
	now     time.Time
}

func newCatalogSecurityWorkflow(t *testing.T) *catalogSecurityWorkflow {
	t.Helper()
	db, review := controlledFixture(t)
	f := &catalogSecurityWorkflow{db: db, scope: catalog.Scope{Tenant: review.Tenant, MoyskladAccount: review.MoyskladAccount, OzonAccount: review.OzonAccount}, now: time.Now().UTC()}
	for _, account := range []catalog.Account{{Provider: catalog.ProviderMoySklad, ID: f.scope.MoyskladAccount}, {Provider: catalog.ProviderOzon, ID: f.scope.OzonAccount}} {
		f.saveCredential(t, account, 0)
	}
	f.config = f.initialConfig(t)
	f.source = &catalogSecurityProvider{product: catalog.Product{Identity: f.config.Targets[0].Source, Revision: "source-reviewed", ObservedAt: f.now, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Reviewed source name")}}}
	f.target = &catalogSecurityProvider{product: catalog.Product{Identity: f.config.Targets[0].Target, Revision: "target-reviewed", ObservedAt: f.now, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Original target name")}}}
	resolver := catalogsync.ResolveFunc(func(_ context.Context, scope catalog.Scope, account catalog.Account, revision int64) (catalogsync.Binding, error) {
		client := f.target
		if account == f.source.product.Identity.Account {
			client = f.source
		}
		return catalogsync.Binding{Scope: scope, Account: account, CredentialRevision: revision, Client: client}, nil
	})
	f.service = catalogsync.Service{Store: db, Resolver: resolver, AllowScope: func(scope catalog.Scope) bool { return scope == f.scope }, Now: func() time.Time { return f.now }}
	f.runner = catalogsync.Runner{Enabled: true, WorkerID: "synthetic-security-worker", Store: db, Resolver: resolver, AllowScope: func(scope catalog.Scope) bool { return scope == f.scope }, Now: func() time.Time { return f.now }}
	config, err := f.service.SaveConfig(t.Context(), catalogsync.SaveConfigRequest{Config: f.config, Actor: "synthetic-security-actor", IdempotencyKey: "security-initial-config"})
	if err != nil {
		t.Fatal(err)
	}
	f.config = config
	return f
}

func (f *catalogSecurityWorkflow) initialConfig(t *testing.T) catalogsync.Config {
	t.Helper()
	var id int64
	var version int64
	var kind, source, product, offer string
	err := f.db.Pool.QueryRow(t.Context(), `SELECT id,version,target_kind,target_id,ozon_product_id,ozon_offer_id FROM product_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, f.scope.Tenant, f.scope.MoyskladAccount, f.scope.OzonAccount).Scan(&id, &version, &kind, &source, &product, &offer)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := catalogsync.Enrollment{ID: "security-enrollment", MappingID: strconv.FormatInt(id, 10), MappingVersion: version,
		Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: f.scope.MoyskladAccount}, EntityType: kind, ID: source},
		Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: f.scope.OzonAccount}, EntityType: "product", ID: product, OfferID: offer}}
	return catalogsync.Config{Scope: f.scope, Enabled: true, TestEnabled: true, IntervalSeconds: 30,
		Rules: catalog.Policies{catalog.FieldName: catalog.MoySkladAuthoritative}, Targets: []catalogsync.Enrollment{enrollment}}
}

func (f *catalogSecurityWorkflow) saveCredential(t *testing.T, account catalog.Account, previous int64) {
	t.Helper()
	vault, err := credentials.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := credentials.Scope{Tenant: f.scope.Tenant, Provider: string(account.Provider), Account: account.ID}
	ciphertext, err := vault.Encrypt(scope, previous+1, "synthetic-security-token")
	if err != nil {
		t.Fatal(err)
	}
	key := "security-credential-" + string(account.Provider) + "-" + strconv.FormatInt(previous, 10)
	if _, _, err = f.db.SaveCredential(t.Context(), scope, "Synthetic security account", previous, key, vault.Digest([]byte(key)), ciphertext, f.now, "synthetic-security-actor"); err != nil {
		t.Fatal(err)
	}
}

func (f *catalogSecurityWorkflow) preview(t *testing.T) catalogsync.Preview {
	t.Helper()
	preview, err := f.service.Preview(t.Context(), catalogsync.PreviewRequest{Scope: f.scope, ExpectedRevision: f.config.Revision, Actor: "synthetic-security-actor"})
	if err != nil || len(preview.Decisions) != 1 {
		t.Fatalf("create security preview: decisions=%d error=%v", len(preview.Decisions), err)
	}
	return preview
}

func (f *catalogSecurityWorkflow) approve(t *testing.T) catalogsync.Operation {
	t.Helper()
	preview := f.preview(t)
	op, err := f.service.Approve(t.Context(), catalogsync.Approval{Scope: f.scope, PreviewID: preview.ID, Actor: "synthetic-security-actor", IdempotencyKey: "security-approval"})
	if err != nil || len(op.Targets) != 1 {
		t.Fatalf("approve security preview: targets=%d error=%v", len(op.Targets), err)
	}
	return op
}

func (f *catalogSecurityWorkflow) run(t *testing.T) {
	t.Helper()
	f.now = f.now.Add(time.Second)
	worked, err := f.runner.RunNext(t.Context())
	if err != nil || !worked {
		t.Fatalf("advance security operation: worked=%v error=%v", worked, err)
	}
}

func (f *catalogSecurityWorkflow) work(t *testing.T) catalogsync.Work {
	t.Helper()
	page, err := f.db.CatalogHistory(t.Context(), f.scope, catalogsync.PageRequest{Limit: 10})
	if err != nil || len(page.Operations) != 1 || len(page.Operations[0].Targets) != 1 {
		t.Fatalf("read security operation: operations=%d error=%v", len(page.Operations), err)
	}
	return page.Operations[0].Targets[0]
}

func TestCatalogSecurityDispatchJournalsBeforeOneAsyncSend(t *testing.T) {
	f := newCatalogSecurityWorkflow(t)
	f.target.result = func(command catalog.Command) (catalog.Receipt, error) {
		payload, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		var attempts int
		err = f.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_sync_attempts a JOIN catalog_sync_work w ON w.id=a.work_id JOIN catalog_sync_commands c ON c.work_id=w.id WHERE c.command_id=$1 AND c.command=$2::jsonb AND w.state='DISPATCHING' AND w.dispatch_count=1 AND a.phase='DISPATCHING'`, command.ID, payload).Scan(&attempts)
		if err != nil || attempts != 1 {
			t.Fatalf("provider dispatch began before a committed durable intent: attempts=%d error=%v", attempts, err)
		}
		return catalogSecurityAccepted(command), nil
	}
	f.approve(t)
	f.run(t)
	work := f.work(t)
	if f.target.writes != 1 || work.State != catalogsync.StateProcessing || work.Receipt.TaskID != "501" {
		t.Fatalf("guarded async dispatch failed: writes=%d state=%s task=%q code=%s", f.target.writes, work.State, work.Receipt.TaskID, work.Code)
	}
	var dispatches, attempts int
	err := f.db.Pool.QueryRow(t.Context(), `SELECT dispatch_count,(SELECT count(*) FROM catalog_sync_attempts WHERE work_id=$1 AND phase='DISPATCHING') FROM catalog_sync_work WHERE id=$1`, work.ID).Scan(&dispatches, &attempts)
	if err != nil || dispatches != 1 || attempts != 1 {
		t.Fatalf("mutation was not durably journalled once: dispatches=%d attempts=%d error=%v", dispatches, attempts, err)
	}
}

func TestCatalogSecurityUnknownWriteFencesTargetWithoutReplay(t *testing.T) {
	f := newCatalogSecurityWorkflow(t)
	f.target.result = func(command catalog.Command) (catalog.Receipt, error) {
		return catalog.Receipt{CommandID: command.ID, State: catalog.ReceiptUnknown}, errors.New("synthetic uncertain network outcome")
	}
	f.approve(t)
	f.run(t)
	if work := f.work(t); work.State != catalogsync.StateUnknown || f.target.writes != 1 {
		t.Fatalf("uncertain write not fenced: state=%s writes=%d", work.State, f.target.writes)
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.runner.RunNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	preview := f.preview(t)
	if f.target.writes != 1 || preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("unknown outcome was resent or became a new approvable decision")
	}
}
