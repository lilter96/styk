package orderflow

import (
	"context"
	"fmt"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

// RealizationReport is an explicit read-only owner preview. It does not enroll a
// background importer or extend the mirror automation grant to accounting writes.
func (s *Service) RealizationReport(ctx context.Context, request orders.RealizationRequest) (orders.RealizationReport, error) {
	if request.Scope.Validate() != nil {
		return orders.RealizationReport{}, orders.ErrInvalid
	}
	if err := request.Period.Validate(s.now()); err != nil {
		return orders.RealizationReport{}, err
	}
	config, err := s.financeReadConfig(ctx, request.Scope)
	if err != nil {
		return orders.RealizationReport{}, err
	}
	binding, err := s.resolver.ResolveOrder(ctx, request.Scope, "ozon", request.Scope.OzonAccount, 0)
	if err != nil {
		return orders.RealizationReport{}, err
	}
	reader, err := realizationBinding(binding, request.Scope)
	if err != nil {
		return orders.RealizationReport{}, err
	}
	guard := func(attempt context.Context) error {
		return s.guardFinanceRead(attempt, config, binding.CredentialRevision)
	}
	if err = guard(ctx); err != nil {
		return orders.RealizationReport{}, err
	}
	report, err := reader.ReadRealizationReport(providercall.WithGuard(ctx, guard), request.Scope, request.Period)
	if err != nil {
		return orders.RealizationReport{}, fmt.Errorf("preview authoritative monthly realization: %w", err)
	}
	if report.Scope != request.Scope || report.Period != request.Period || report.Fingerprint == "" {
		return orders.RealizationReport{}, orders.ErrStale
	}
	if err = guard(ctx); err != nil {
		return orders.RealizationReport{}, err
	}
	return s.classifiedRealization(ctx, report)
}

func (s *Service) classifiedRealization(ctx context.Context, report orders.RealizationReport) (orders.RealizationReport, error) {
	analysis, err := ordermoney.ClassifyRealization(report)
	if err != nil {
		return orders.RealizationReport{}, err
	}
	report.Analysis = &analysis
	if observer, ok := s.store.(interface {
		ObserveCommissionReport(context.Context, orders.RealizationReport) error
	}); ok {
		if err = observer.ObserveCommissionReport(ctx, report); err != nil {
			return orders.RealizationReport{}, err
		}
	}
	return report, nil
}

func realizationBinding(binding orders.Binding, scope orders.Scope) (orders.RealizationReader, error) {
	if binding.Scope != scope || binding.Provider != "ozon" || binding.Account != scope.OzonAccount || binding.CredentialRevision < 1 {
		return nil, orders.ErrStale
	}
	reader, ok := binding.Client.(orders.RealizationReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader, nil
}

func (s *Service) financeReadConfig(ctx context.Context, scope orders.Scope) (orders.Config, error) {
	if !s.allowed(scope) {
		return orders.Config{}, orders.ErrBlocked
	}
	config, err := s.store.OrderConfig(ctx, scope)
	if err != nil {
		return orders.Config{}, err
	}
	if config.Scope != scope {
		return orders.Config{}, orders.ErrStale
	}
	if !config.ReadsEnabled() {
		return orders.Config{}, orders.ErrBlocked
	}
	return config, nil
}

func (s *Service) guardFinanceRead(ctx context.Context, expected orders.Config, revision int64) error {
	config, err := s.financeReadConfig(ctx, expected.Scope)
	if err != nil {
		return err
	}
	if config.Revision != expected.Revision {
		return orders.ErrStale
	}
	binding, err := s.resolver.ResolveOrder(ctx, expected.Scope, "ozon", expected.Scope.OzonAccount, revision)
	if err != nil {
		return err
	}
	if binding.CredentialRevision != revision {
		return orders.ErrStale
	}
	_, err = realizationBinding(binding, expected.Scope)
	return err
}
