package orderflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type metadataInitializationStore struct {
	registry       orders.MetadataRegistry
	intent         orders.MetadataCreationIntent
	begins, guards int
	sent, denied   bool
	completions    []orders.MetadataCreationCompletion
}

func (store *metadataInitializationStore) OrderMetadata(_ context.Context, scope orders.Scope) (orders.MetadataRegistry, error) {
	store.registry.Scope = scope
	return store.registry, nil
}
func (*metadataInitializationStore) SaveOrderMetadata(context.Context, orders.MetadataRegistry, int64) (orders.MetadataRegistry, error) {
	return orders.MetadataRegistry{}, errors.New("setup must complete the separate metadata intent")
}
func (store *metadataInitializationStore) BeginOrderMetadataCreation(_ context.Context, request orders.MetadataCreationRequest, now time.Time) (orders.MetadataCreationIntent, error) {
	store.begins++
	if store.intent.ID != "" {
		if request.Scope != store.intent.Scope || request.UniqueCode != store.intent.UniqueCode || request.Type != store.intent.Type || request.IdempotencyKey != store.intent.IdempotencyKey {
			return orders.MetadataCreationIntent{}, orders.ErrConflict
		}
		intent := store.intent
		intent.DispatchPermitted = false
		return intent, nil
	}
	id := "0123456789abcdef0123456789abcdef"
	store.intent = orders.MetadataCreationIntent{ID: id, Scope: request.Scope, UniqueCode: request.UniqueCode, Type: request.Type, Marker: "styk-order-attribute-" + id, State: orders.StateDispatching, IdempotencyKey: request.IdempotencyKey, CreatedAt: now}
	intent := store.intent
	intent.DispatchPermitted = true
	return intent, nil
}
func (store *metadataInitializationStore) GuardOrderMetadataCreation(_ context.Context, intent orders.MetadataCreationIntent, actor string, revision int64) (bool, error) {
	store.guards++
	if store.denied || store.sent || intent.ID != store.intent.ID || actor != "actor" || revision != 2 || !intent.DispatchPermitted {
		return false, orders.ErrBlocked
	}
	store.sent = true
	return true, nil
}
func (store *metadataInitializationStore) CompleteOrderMetadataCreation(_ context.Context, completion orders.MetadataCreationCompletion) (orders.MetadataRegistry, error) {
	if completion.IntentID != store.intent.ID || completion.ExpectedState != store.intent.State {
		return orders.MetadataRegistry{}, orders.ErrStale
	}
	store.completions = append(store.completions, completion)
	store.intent.State, store.intent.AttributeID = completion.State, completion.AttributeID
	if completion.State == orders.StateReconciled {
		store.registry.Revision++
		store.registry.CreationIntentID, store.registry.ObservedAt = completion.IntentID, completion.ObservedAt
		store.registry.Attributes = append(store.registry.Attributes, orders.AttributeDefinition{UniqueCode: store.intent.UniqueCode, AttributeID: completion.AttributeID, Type: store.intent.Type})
	}
	return store.registry, nil
}

type metadataInitializationProvider struct {
	*flowProvider
	store               *metadataInitializationStore
	unknown             bool
	diagnostic          string
	creates, recoveries int
}

func (provider *metadataInitializationProvider) CreateOrderAttribute(ctx context.Context, scope orders.Scope, intent orders.MetadataCreationIntent, authorize func(context.Context) (bool, error)) (orders.MetadataCreationCompletion, error) {
	completion := orders.MetadataCreationCompletion{IntentID: intent.ID, Scope: scope, ExpectedState: intent.State, State: orders.StateUnknown, ObservedAt: flowTestNow}
	completion.Code = provider.diagnostic
	if intent.ID == "" || provider.store.intent.ID != intent.ID {
		return completion, orders.ErrInvalid
	}
	if intent.DispatchPermitted {
		allowed, err := authorize(ctx)
		if err != nil || !allowed {
			completion.State = orders.StateRejected
			return completion, orders.ErrBlocked
		}
		provider.creates++
	} else {
		provider.recoveries++
	}
	if provider.unknown {
		return completion, orders.ErrBlocked
	}
	completion.State, completion.AttributeID = orders.StateReconciled, "11111111-1111-4111-8111-111111111111"
	return completion, nil
}

type metadataInitializationResolver struct {
	provider *metadataInitializationProvider
	badScope bool
}

func (resolver metadataInitializationResolver) ResolveOrder(_ context.Context, scope orders.Scope, provider, account string, _ int64) (orders.Binding, error) {
	if resolver.badScope {
		scope.Tenant = "other-tenant"
	}
	return orders.Binding{Scope: scope, Provider: provider, Account: account, CredentialRevision: 2, Client: resolver.provider}, nil
}

func metadataInitializerFixture(t *testing.T) (*MetadataInitializer, *metadataInitializationStore, *metadataInitializationProvider, orders.Scope) {
	t.Helper()
	store := &metadataInitializationStore{}
	provider := &metadataInitializationProvider{flowProvider: &flowProvider{}, store: store}
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	service, err := NewMetadataInitializer(store, metadataInitializationResolver{provider: provider}, ServiceOptions{AllowScope: func(candidate orders.Scope) bool { return candidate == scope }, Now: func() time.Time { return flowTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, provider, scope
}

func TestMetadataInitializerJournalsOneIntentBeforeCreation(t *testing.T) {
	t.Parallel()
	service, store, provider, scope := metadataInitializerFixture(t)
	result, err := service.EnsureMetadata(context.Background(), scope, "styk-order-status", "string", "setup-status", "actor")
	if err != nil || result.State != orders.StateReconciled || result.IntentID == "" || len(result.Registry.Attributes) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if provider.creates != 1 || store.guards != 1 || len(store.completions) != 1 {
		t.Fatal("creation was not bound to one journaled intent")
	}
	result, err = service.EnsureMetadata(context.Background(), scope, "styk-order-status", "string", "another-key", "actor")
	if err != nil || result.State != orders.StateReconciled || provider.creates != 1 || store.begins != 1 {
		t.Fatal("registered native UUID was recreated")
	}
}

func TestMetadataInitializerUnknownRecoveryNeverCreatesAgain(t *testing.T) {
	t.Parallel()
	service, store, provider, scope := metadataInitializerFixture(t)
	provider.unknown = true
	result, err := service.EnsureMetadata(context.Background(), scope, "styk-order-id", "string", "setup-id", "actor")
	if err != nil || result.State != orders.StateUnknown || result.Code != "METADATA_OUTCOME_UNKNOWN" || provider.creates != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	result, err = service.EnsureMetadata(context.Background(), scope, "styk-order-id", "string", "setup-id", "actor")
	if err != nil || result.State != orders.StateUnknown || provider.creates != 1 || store.guards != 1 || provider.recoveries != 1 {
		t.Fatal("unknown creation was resent")
	}
	provider.unknown = false
	result, err = service.EnsureMetadata(context.Background(), scope, "styk-order-id", "string", "setup-id", "actor")
	if err != nil || result.State != orders.StateReconciled || provider.creates != 1 || len(result.Registry.Attributes) != 1 {
		t.Fatalf("read-only recovery=%+v err=%v", result, err)
	}
}

func TestMetadataInitializerRejectsPIIUnsupportedTypesAndForeignScopes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, code, kind string
		foreign          bool
	}{{"customer pii", "customer.name", "string", false}, {"unsupported type", "styk-order-status", "long", false}, {"foreign scope", "styk-order-status", "string", true}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, store, provider, scope := metadataInitializerFixture(t)
			if test.foreign {
				scope.Tenant = "other"
			}
			if _, err := service.EnsureMetadata(context.Background(), scope, test.code, test.kind, "setup", "actor"); err == nil || store.begins != 0 || provider.creates != 0 {
				t.Fatal("unreviewed metadata intent admitted")
			}
		})
	}
}

func TestMetadataInitializerDenialIsPersistedWithoutProviderWrite(t *testing.T) {
	t.Parallel()
	service, store, provider, scope := metadataInitializerFixture(t)
	store.denied = true
	result, err := service.EnsureMetadata(context.Background(), scope, "styk-order-substatus", "text", "setup-substatus", "actor")
	if err != nil || result.State != orders.StateRejected || provider.creates != 0 || len(store.completions) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMetadataInitializerPreservesReadbackDiagnosticWhenReconciled(t *testing.T) {
	t.Parallel()
	service, store, provider, scope := metadataInitializerFixture(t)
	provider.diagnostic = "CONFIRMED_BY_MARKER_AFTER_RESPONSE_DRIFT"
	result, err := service.EnsureMetadata(context.Background(), scope, "styk-order-status", "string", "setup-status", "actor")
	if err != nil || result.State != orders.StateReconciled || result.Code != provider.diagnostic || len(store.completions) != 1 || store.completions[0].Code != provider.diagnostic {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
