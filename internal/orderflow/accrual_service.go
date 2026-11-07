package orderflow

import (
	"context"
	"fmt"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type accrualReviewer interface {
	CarriageReviewContext(context.Context, orders.Scope, string) (orders.ReviewContext, error)
}

func (s *Service) AccrualReport(ctx context.Context, scope orders.Scope, request orders.AccrualRequest, actor string) (orders.AccrualReport, error) {
	if scope.Validate() != nil || request.Validate(s.now()) != nil || actor == "" {
		return orders.AccrualReport{}, orders.ErrInvalid
	}
	store, ok := s.store.(accrualReviewer)
	if !ok || !s.allowed(scope) {
		return orders.AccrualReport{}, orders.ErrBlocked
	}
	review, err := store.CarriageReviewContext(ctx, scope, actor)
	if err != nil {
		return orders.AccrualReport{}, err
	}
	binding, err := s.resolver.ResolveOrder(ctx, scope, "ozon", scope.OzonAccount, review.Credentials.Ozon)
	if err != nil {
		return orders.AccrualReport{}, err
	}
	reader, err := accrualBinding(binding, scope)
	if err != nil {
		return orders.AccrualReport{}, err
	}
	guard := func(check context.Context) error {
		return s.guardAccrualRead(check, scope, actor, review.Config.Revision, review.Credentials)
	}
	if err = guard(ctx); err != nil {
		return orders.AccrualReport{}, err
	}
	report, err := reader.ReadAccrualReport(providercall.WithGuard(ctx, guard), scope, request)
	if err != nil {
		return orders.AccrualReport{}, fmt.Errorf("read separate accrual diagnostic: %w", err)
	}
	if report.Scope != scope || report.SourceFamily != "ACCRUAL" || report.Kind != request.Kind || len(report.Fingerprint) != 64 {
		return orders.AccrualReport{}, orders.ErrStale
	}
	if err = guard(ctx); err != nil {
		return orders.AccrualReport{}, err
	}
	return report, nil
}

func accrualBinding(binding orders.Binding, scope orders.Scope) (orders.AccrualReader, error) {
	if binding.Scope != scope || binding.Provider != "ozon" || binding.Account != scope.OzonAccount || binding.CredentialRevision < 1 {
		return nil, orders.ErrStale
	}
	reader, ok := binding.Client.(orders.AccrualReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader, nil
}

func (s *Service) guardAccrualRead(ctx context.Context, scope orders.Scope, actor string, revision int64, credentials orders.CredentialRevisions) error {
	store, ok := s.store.(accrualReviewer)
	if !ok || !s.allowed(scope) {
		return orders.ErrBlocked
	}
	current, err := store.CarriageReviewContext(ctx, scope, actor)
	if err != nil {
		return err
	}
	if current.Config.Scope != scope || current.Config.Revision != revision || current.Credentials != credentials {
		return orders.ErrStale
	}
	return nil
}
