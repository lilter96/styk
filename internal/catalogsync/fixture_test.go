package catalogsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

var errTestProvider = errors.New("synthetic provider unavailable")

type fixture struct {
	ctx     context.Context
	store   *memoryStore
	source  *memoryProvider
	target  *memoryProvider
	service Service
	runner  Runner
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	scope := catalog.Scope{Tenant: "tenant-test", MoyskladAccount: "moy-test", OzonAccount: "ozon-test"}
	source := testProduct(scope, catalog.ProviderMoySklad, "source-1", "new name")
	target := testProduct(scope, catalog.ProviderOzon, "target-1", "old name")
	mapping := catalog.Mapping{ID: "mapping-1", Version: 1, Source: source.Identity, Target: target.Identity, Confirmed: true}
	enrollment := Enrollment{ID: "enrollment-1", MappingID: mapping.ID, MappingVersion: 1, Source: source.Identity, Target: target.Identity}
	config := Config{Scope: scope, Revision: 1, Enabled: true, TestEnabled: true, IntervalSeconds: 60,
		Rules: catalog.Policies{catalog.FieldName: catalog.MoySkladAuthoritative}, Targets: []Enrollment{enrollment}}
	store := &memoryStore{current: ReviewContext{Config: config, Credentials: CredentialRevisions{Moysklad: 1, Ozon: 2},
		Targets: []TargetContext{{Enrollment: enrollment, Mapping: &mapping}}}, previews: make(map[string]Preview), approvals: make(map[string]Operation)}
	f := &fixture{ctx: context.Background(), store: store, source: newMemoryProvider(source), target: newMemoryProvider(target), now: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	resolver := ResolveFunc(f.resolve)
	allow := func(candidate catalog.Scope) bool { return candidate == scope }
	f.service = Service{Store: store, Resolver: resolver, AllowScope: allow, Now: func() time.Time { return f.now }}
	f.runner = Runner{Enabled: true, WorkerID: "worker-test", Store: store, Resolver: resolver, AllowScope: allow, Now: f.service.Now}
	return f
}

func testProduct(scope catalog.Scope, provider catalog.Provider, id, name string) catalog.Product {
	account := scope.MoyskladAccount
	if provider == catalog.ProviderOzon {
		account = scope.OzonAccount
	}
	return catalog.Product{Identity: catalog.Identity{Account: catalog.Account{Provider: provider, ID: account}, EntityType: "product", ID: id,
		OfferID: "offer-" + id}, Lifecycle: catalog.LifecycleActive, Fields: map[catalog.Field]catalog.Value{
		catalog.FieldName: catalog.Text(name), catalog.FieldDescription: catalog.Text("description")}, Revision: "revision-" + id}
}

func (f *fixture) resolve(_ context.Context, scope catalog.Scope, account catalog.Account, revision int64) (Binding, error) {
	var client Provider = f.target
	if account.Provider == catalog.ProviderMoySklad {
		client = f.source
	}
	return Binding{Scope: scope, Account: account, CredentialRevision: revision, Client: client}, nil
}

func (f *fixture) preview(t *testing.T) Preview {
	t.Helper()
	preview, err := f.service.Preview(f.ctx, PreviewRequest{Scope: f.store.current.Config.Scope, ExpectedRevision: f.store.current.Config.Revision, Actor: "actor-test"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	return preview
}

func (f *fixture) approve(t *testing.T) Operation {
	t.Helper()
	preview := f.preview(t)
	operation, err := f.service.Approve(f.ctx, Approval{Scope: preview.Scope, PreviewID: preview.ID, Actor: "actor-test", IdempotencyKey: "approve-test"})
	if err != nil {
		t.Fatalf("approve: %v; decisions=%+v", err, preview.Decisions)
	}
	return operation
}

func (f *fixture) run(t *testing.T) {
	t.Helper()
	f.now = f.now.Add(time.Minute)
	worked, err := f.runner.RunNext(f.ctx)
	if err != nil || !worked {
		t.Fatalf("runner worked=%v, error=%v", worked, err)
	}
}

func clone[T any](input T) T {
	data, err := json.Marshal(input)
	if err != nil {
		return input
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return input
	}
	return result
}

type memoryProvider struct {
	products      map[string]catalog.Product
	markers       map[string]string
	guard         func(context.Context) (bool, error)
	writes        int
	polls         int
	writeErr      error
	readErr       error
	applyOnError  bool
	accept        bool
	bindHook      func()
	pollState     catalog.ReceiptState
	commands      []catalog.Command
	writeReceipt  *catalog.Receipt
	validation    []catalog.FieldError
	validationErr error
	pollReceipt   *catalog.Receipt
}

func newMemoryProvider(product catalog.Product) *memoryProvider {
	return &memoryProvider{products: map[string]catalog.Product{product.Identity.ID: clone(product)}, markers: make(map[string]string), pollState: catalog.ReceiptApplied}
}

func (provider *memoryProvider) ReadCatalog(_ context.Context, request catalog.ReadRequest) (catalog.Snapshot, error) {
	if provider.readErr != nil {
		return catalog.Snapshot{}, provider.readErr
	}
	if request.Search != "" || len(request.Identities) != 1 {
		return catalog.Snapshot{}, ErrInvalid
	}
	identity := request.Identities[0]
	snapshot := catalog.Snapshot{Scope: request.Scope, Account: request.Account, Complete: true, Products: make([]catalog.Product, 0, 1)}
	for _, product := range provider.products {
		if identity.ID == product.Identity.ID || (identity.ID == "" && identity.OfferID != "" && identity.OfferID == product.Identity.OfferID) {
			snapshot.Products = append(snapshot.Products, clone(product))
		}
	}
	return snapshot, nil
}

func (provider *memoryProvider) LookupCatalogCreate(_ context.Context, command catalog.Command) (catalog.Snapshot, error) {
	if provider.readErr != nil {
		return catalog.Snapshot{}, provider.readErr
	}
	snapshot := catalog.Snapshot{Scope: command.Scope, Account: command.Target.Account, Complete: true, Products: make([]catalog.Product, 0, 1)}
	if id := provider.markers[command.CreateExternalCode]; id != "" {
		snapshot.Products = append(snapshot.Products, clone(provider.products[id]))
	}
	return snapshot, nil
}

func (provider *memoryProvider) ValidateCatalog(context.Context, catalog.Command) ([]catalog.FieldError, error) {
	return provider.validation, provider.validationErr
}

func (provider *memoryProvider) BindCatalogIntent(_ catalog.Command, guard func(context.Context) (bool, error)) error {
	provider.guard = guard
	if provider.bindHook != nil {
		provider.bindHook()
	}
	return nil
}

func (provider *memoryProvider) WriteCatalog(ctx context.Context, command catalog.Command) (catalog.Receipt, error) {
	if provider.guard == nil {
		return catalog.Receipt{}, ErrBlocked
	}
	allowed, err := provider.guard(ctx)
	if err != nil || !allowed {
		return catalog.Receipt{}, ErrBlocked
	}
	provider.writes++
	provider.commands = append(provider.commands, clone(command))
	if provider.writeReceipt != nil {
		receipt := clone(*provider.writeReceipt)
		receipt.CommandID = command.ID
		return receipt, provider.writeErr
	}
	if provider.writeErr != nil {
		if provider.applyOnError {
			provider.apply(command)
		}
		return catalog.Receipt{}, provider.writeErr
	}
	if provider.accept {
		return catalog.Receipt{CommandID: command.ID, State: catalog.ReceiptAccepted, TaskID: "task-1"}, nil
	}
	identity := provider.apply(command)
	return catalog.Receipt{CommandID: command.ID, State: catalog.ReceiptApplied, ExternalID: identity.ID}, nil
}

func (provider *memoryProvider) PollCatalog(_ context.Context, command catalog.Command, previous catalog.Receipt) (catalog.Receipt, error) {
	provider.polls++
	if provider.pollReceipt != nil {
		receipt := clone(*provider.pollReceipt)
		receipt.CommandID, receipt.TaskID = command.ID, previous.TaskID
		return receipt, nil
	}
	receipt := catalog.Receipt{CommandID: command.ID, State: provider.pollState, TaskID: previous.TaskID}
	if provider.pollState == catalog.ReceiptApplied {
		identity := provider.apply(command)
		receipt.ExternalID = identity.ID
	}
	return receipt, nil
}

func (provider *memoryProvider) apply(command catalog.Command) catalog.Identity {
	identity := command.Target
	if command.Action == catalog.ActionCreate {
		identity.ID = "provider-created-1"
		provider.markers[command.CreateExternalCode] = identity.ID
	}
	product, ok := provider.products[identity.ID]
	if !ok {
		product = catalog.Product{Identity: identity, Lifecycle: catalog.LifecycleActive, Fields: make(map[catalog.Field]catalog.Value)}
	}
	for field, value := range command.Values {
		if value.State == catalog.StateClear {
			value = catalog.Empty()
		}
		product.Fields[field] = value
	}
	product.Revision = fmt.Sprintf("applied-%d", len(provider.commands))
	product.Parent, product.Extensions = command.Parent, clone(command.Extensions)
	provider.products[identity.ID] = clone(product)
	return identity
}
