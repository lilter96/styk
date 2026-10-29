package orderflow

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type metadataRecoveryReader interface {
	ReadOrderMetadataCreation(context.Context, orders.Scope, string, string) (orders.MetadataCreationIntent, error)
}

// ReconcileMetadata can only observe an already journaled intent. It cannot
// create one, grant dispatch or depend on a previous browser session remaining
// valid. The current authenticated merchant must own the original target.
func (s *MetadataInitializer) ReconcileMetadata(ctx context.Context, scope orders.Scope, code, actor string) (MetadataSetupResult, error) {
	if err := s.validateMetadataSetup(scope, code, "string", "read-reconciliation", actor); err != nil {
		return MetadataSetupResult{}, err
	}
	store, ok := s.store.(metadataRecoveryReader)
	if !ok {
		return MetadataSetupResult{}, orders.ErrBlocked
	}
	intent, err := store.ReadOrderMetadataCreation(ctx, scope, code, actor)
	if err != nil {
		return MetadataSetupResult{}, err
	}
	if intent.Scope != scope || intent.UniqueCode != code || intent.DispatchPermitted {
		return MetadataSetupResult{}, orders.ErrStale
	}
	if intent.State == orders.StateRejected || intent.State == orders.StateReconciled {
		registry, err := s.store.OrderMetadata(ctx, scope)
		return MetadataSetupResult{State: intent.State, Registry: registry, IntentID: intent.ID}, err
	}
	return s.readMetadataRecovery(ctx, intent)
}

func (s *MetadataInitializer) readMetadataRecovery(ctx context.Context, intent orders.MetadataCreationIntent) (MetadataSetupResult, error) {
	binding, err := s.resolver.ResolveOrder(ctx, intent.Scope, "moysklad", intent.Scope.MoyskladAccount, 0)
	if err != nil {
		return MetadataSetupResult{}, err
	}
	creator, err := metadataBinding(binding, intent.Scope)
	if err != nil {
		return MetadataSetupResult{}, err
	}
	denyDispatch := func(context.Context) (bool, error) { return false, orders.ErrBlocked }
	completion, providerErr := creator.CreateOrderAttribute(ctx, intent.Scope, intent, denyDispatch)
	if !metadataCompletionMatches(intent, completion) {
		return MetadataSetupResult{}, orders.ErrStale
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	registry, err := s.store.CompleteOrderMetadataCreation(cleanup, completion)
	if err != nil {
		return MetadataSetupResult{}, fmt.Errorf("record metadata read reconciliation: %w", err)
	}
	result := MetadataSetupResult{State: completion.State, Registry: registry, IntentID: intent.ID}
	if completion.State != orders.StateReconciled {
		result.Code = metadataOutcomeCode(providerErr, completion.State)
	}
	return result, nil
}
