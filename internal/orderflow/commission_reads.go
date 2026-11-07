package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func (s *Service) CommissionMetadata(ctx context.Context, scope orders.Scope) (orders.CommissionMetadata, error) {
	config, err := s.financeReadConfig(ctx, scope)
	if err != nil {
		return orders.CommissionMetadata{}, err
	}
	binding, err := s.resolver.ResolveOrder(ctx, scope, "moysklad", scope.MoyskladAccount, 0)
	if err != nil {
		return orders.CommissionMetadata{}, err
	}
	reader, err := commissionReaderBinding(binding, scope)
	if err != nil {
		return orders.CommissionMetadata{}, err
	}
	credentials := orders.CredentialRevisions{Moysklad: binding.CredentialRevision}
	guard := func(check context.Context) error { return s.guardCommissionConfig(check, config, credentials) }
	if err = guard(ctx); err != nil {
		return orders.CommissionMetadata{}, err
	}
	result, err := reader.ReadCommissionMetadata(providercall.WithGuard(ctx, guard), scope)
	if err != nil {
		return orders.CommissionMetadata{}, err
	}
	if result.Scope != scope {
		return orders.CommissionMetadata{}, orders.ErrStale
	}
	if err = guard(ctx); err != nil {
		return orders.CommissionMetadata{}, err
	}
	return result, nil
}

func commissionReaderBinding(binding orders.Binding, scope orders.Scope) (orders.CommissionReader, error) {
	if binding.Scope != scope || binding.Provider != "moysklad" || binding.Account != scope.MoyskladAccount || binding.CredentialRevision < 1 {
		return nil, orders.ErrStale
	}
	reader, ok := binding.Client.(orders.CommissionReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader, nil
}

func (s *Service) commissionReferences(ctx context.Context, review orders.ReviewContext) (orders.CommissionReferences, error) {
	if review.Config.Finance == nil || !review.Config.Finance.Active(s.now(), review.Config.Scope, review.Config.Revision) {
		return orders.CommissionReferences{}, orders.ErrBlocked
	}
	binding, err := s.resolver.ResolveOrder(ctx, review.Config.Scope, "moysklad", review.Config.Scope.MoyskladAccount, review.Credentials.Moysklad)
	if err != nil {
		return orders.CommissionReferences{}, err
	}
	reader, err := commissionReaderBinding(binding, review.Config.Scope)
	if err != nil {
		return orders.CommissionReferences{}, err
	}
	if binding.CredentialRevision != review.Credentials.Moysklad {
		return orders.CommissionReferences{}, orders.ErrStale
	}
	guard := func(check context.Context) error {
		return s.guardCommissionConfig(check, review.Config, review.Credentials)
	}
	if err = guard(ctx); err != nil {
		return orders.CommissionReferences{}, err
	}
	return reader.ReadCommissionReferences(providercall.WithGuard(ctx, guard), review.Config.Scope, *review.Config.Finance)
}

func (s *Service) guardCommissionConfig(ctx context.Context, expected orders.Config, credentials orders.CredentialRevisions) error {
	current, err := s.financeReadConfig(ctx, expected.Scope)
	if err != nil {
		return err
	}
	if current.Revision != expected.Revision {
		return orders.ErrStale
	}
	store, ok := s.store.(commissionReviewer)
	if !ok {
		return orders.ErrBlocked
	}
	facts, err := store.CommissionReviewContext(ctx, expected.Scope)
	if err != nil {
		return err
	}
	if facts.Config.Scope != expected.Scope || facts.Config.Revision != expected.Revision || credentials.Ozon > 0 && facts.Credentials.Ozon != credentials.Ozon || credentials.Moysklad > 0 && facts.Credentials.Moysklad != credentials.Moysklad {
		return orders.ErrStale
	}
	return nil
}
