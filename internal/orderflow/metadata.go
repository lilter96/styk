package orderflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type MetadataInitializationStore interface {
	orders.MetadataCreator
	orders.MetadataRegistrar
	GuardOrderMetadataCreation(context.Context, orders.MetadataCreationIntent, string, int64) (bool, error)
}

type NativeMetadataCreator interface {
	CreateOrderAttribute(context.Context, orders.Scope, orders.MetadataCreationIntent, func(context.Context) (bool, error)) (orders.MetadataCreationCompletion, error)
}

type MetadataSetupResult struct {
	State    orders.State            `json:"state"`
	Registry orders.MetadataRegistry `json:"registry"`
	IntentID string                  `json:"intent_id,omitempty"`
	Code     string                  `json:"code,omitempty"`
}

// MetadataInitializer performs explicit setup of three operational fields. It
// has no polling loop, automatic retry, customer PII or customerorder mutation.
type MetadataInitializer struct {
	store    MetadataInitializationStore
	resolver orders.Resolver
	options  ServiceOptions
}

func NewMetadataInitializer(store MetadataInitializationStore, resolver orders.Resolver, options ServiceOptions) (*MetadataInitializer, error) {
	if store == nil || resolver == nil {
		return nil, orders.ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &MetadataInitializer{store: store, resolver: resolver, options: options}, nil
}

func (s *MetadataInitializer) EnsureMetadata(ctx context.Context, scope orders.Scope, uniqueCode, kind, key, actor string) (MetadataSetupResult, error) {
	if err := s.validateMetadataSetup(scope, uniqueCode, kind, key, actor); err != nil {
		return MetadataSetupResult{}, err
	}
	registry, err := s.store.OrderMetadata(ctx, scope)
	if err != nil {
		return MetadataSetupResult{}, err
	}
	if found, err := registeredMetadata(registry, uniqueCode, kind); found || err != nil {
		return MetadataSetupResult{State: orders.StateReconciled, Registry: registry}, err
	}
	binding, err := s.resolver.ResolveOrder(ctx, scope, "moysklad", scope.MoyskladAccount, 0)
	if err != nil {
		return MetadataSetupResult{}, err
	}
	creator, err := metadataBinding(binding, scope)
	if err != nil {
		return MetadataSetupResult{}, err
	}
	request := orders.MetadataCreationRequest{Scope: scope, UniqueCode: uniqueCode, Type: kind, Actor: actor, IdempotencyKey: key}
	intent, err := s.store.BeginOrderMetadataCreation(ctx, request, s.options.Now().UTC())
	if err != nil {
		return MetadataSetupResult{}, err
	}
	if intent.Scope != scope || intent.UniqueCode != uniqueCode || intent.Type != kind {
		return MetadataSetupResult{}, orders.ErrStale
	}
	if intent.State == orders.StateRejected || intent.State == orders.StateReconciled {
		return MetadataSetupResult{State: intent.State, Registry: registry, IntentID: intent.ID}, nil
	}
	return s.initializeMetadata(ctx, intent, actor, binding.CredentialRevision, creator)
}

func (s *MetadataInitializer) validateMetadataSetup(scope orders.Scope, code, kind, key, actor string) error {
	if scope.Validate() != nil || strings.TrimSpace(actor) == "" || strings.TrimSpace(key) != key || len(key) == 0 || len(key) > 128 || strings.ContainsAny(key, "\r\n\x00") {
		return orders.ErrInvalid
	}
	if s.options.AllowScope == nil || !s.options.AllowScope(scope) {
		return orders.ErrBlocked
	}
	if kind != "string" && kind != "text" {
		return orders.ErrInvalid
	}
	switch code {
	case "styk-order-id", "styk-order-status", "styk-order-substatus":
		return nil
	}
	return orders.ErrBlocked
}

func registeredMetadata(registry orders.MetadataRegistry, code, kind string) (bool, error) {
	for _, attribute := range registry.Attributes {
		if attribute.UniqueCode != code {
			continue
		}
		if attribute.Type != kind || attribute.AttributeID == "" {
			return false, orders.ErrConflict
		}
		return true, nil
	}
	return false, nil
}

func metadataBinding(binding orders.Binding, scope orders.Scope) (NativeMetadataCreator, error) {
	if binding.Scope != scope || binding.Provider != "moysklad" || binding.Account != scope.MoyskladAccount || binding.CredentialRevision < 1 {
		return nil, orders.ErrStale
	}
	creator, ok := binding.Client.(NativeMetadataCreator)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return creator, nil
}

func (s *MetadataInitializer) initializeMetadata(ctx context.Context, intent orders.MetadataCreationIntent, actor string, revision int64, creator NativeMetadataCreator) (MetadataSetupResult, error) {
	authorize := func(ctx context.Context) (bool, error) {
		if s.options.AllowScope == nil || !s.options.AllowScope(intent.Scope) {
			return false, orders.ErrBlocked
		}
		return s.store.GuardOrderMetadataCreation(ctx, intent, actor, revision)
	}
	completion, providerErr := creator.CreateOrderAttribute(ctx, intent.Scope, intent, authorize)
	if !metadataCompletionMatches(intent, completion) {
		return MetadataSetupResult{}, orders.ErrStale
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	updated, err := s.store.CompleteOrderMetadataCreation(cleanup, completion)
	if err != nil {
		return MetadataSetupResult{}, fmt.Errorf("record metadata creation outcome: %w", err)
	}
	result := MetadataSetupResult{State: completion.State, Registry: updated, IntentID: intent.ID}
	if completion.State != orders.StateReconciled {
		result.Code = metadataOutcomeCode(providerErr, completion.State)
	}
	if completion.State == orders.StateReconciled {
		found, err := registeredMetadata(updated, intent.UniqueCode, intent.Type)
		if err != nil || !found {
			return MetadataSetupResult{}, orders.ErrStale
		}
	}
	return result, nil
}

func metadataCompletionMatches(intent orders.MetadataCreationIntent, completion orders.MetadataCreationCompletion) bool {
	if completion.IntentID != intent.ID || completion.Scope != intent.Scope || completion.ExpectedState != intent.State || completion.ObservedAt.IsZero() {
		return false
	}
	if completion.State == orders.StateReconciled {
		return completion.AttributeID != ""
	}
	return completion.State == orders.StateUnknown || completion.State == orders.StateRejected
}

func metadataOutcomeCode(err error, state orders.State) string {
	var coded interface{ MetadataCode() string }
	if errors.As(err, &coded) {
		switch code := coded.MetadataCode(); code {
		case "PROVIDER_ADMIN_REQUIRED", "PROVIDER_ATTRIBUTE_REJECTED":
			return code
		}
	}
	if state == orders.StateUnknown {
		return "METADATA_OUTCOME_UNKNOWN"
	}
	return "METADATA_CREATION_REJECTED"
}
