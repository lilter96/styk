package orderflow

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func (store *metadataInitializationStore) ReadOrderMetadataCreation(_ context.Context, scope orders.Scope, code, actor string) (orders.MetadataCreationIntent, error) {
	if store.intent.ID == "" || store.intent.Scope != scope || store.intent.UniqueCode != code || actor == "" {
		return orders.MetadataCreationIntent{}, orders.ErrNotFound
	}
	intent := store.intent
	intent.DispatchPermitted = false
	return intent, nil
}

func TestMetadataReadReconciliationNeverCreatesAnIntent(t *testing.T) {
	t.Parallel()
	service, store, provider, scope := metadataInitializerFixture(t)
	if _, err := service.ReconcileMetadata(context.Background(), scope, "styk-order-id", "new-session"); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("missing intent: %v", err)
	}
	if store.begins != 0 || store.guards != 0 || provider.creates != 0 || provider.recoveries != 0 {
		t.Fatal("read reconciliation initialized or dispatched a native write")
	}
}

func TestMetadataReadReconciliationAcceptsNewAuthorizedSessionWithoutReplay(t *testing.T) {
	t.Parallel()
	service, store, provider, scope := metadataInitializerFixture(t)
	provider.unknown = true
	result, err := service.EnsureMetadata(context.Background(), scope, "styk-order-id", "string", "original-intent", "actor")
	if err != nil || result.State != orders.StateUnknown {
		t.Fatalf("original outcome: %+v %v", result, err)
	}
	provider.unknown = false
	result, err = service.ReconcileMetadata(context.Background(), scope, "styk-order-id", "new-authorized-session")
	if err != nil || result.State != orders.StateReconciled || len(result.Registry.Attributes) != 1 {
		t.Fatalf("observed recovery: %+v %v", result, err)
	}
	if store.begins != 1 || store.guards != 1 || provider.creates != 1 || provider.recoveries != 1 || len(store.completions) != 2 {
		t.Fatal("recovery replayed a create or replaced historical UNKNOWN evidence")
	}
	if store.completions[0].State != orders.StateUnknown || store.completions[1].ExpectedState != orders.StateUnknown {
		t.Fatal("unknown history was rewritten")
	}
	foreign := scope
	foreign.Tenant = "other"
	if _, err = service.ReconcileMetadata(context.Background(), foreign, "styk-order-id", "new-authorized-session"); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("foreign scope: %v", err)
	}
}
