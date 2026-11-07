package orderflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

type financeTestProvider struct {
	*flowProvider
	read func(orders.Scope, orders.RealizationPeriod) (orders.RealizationReport, error)
}

func (p financeTestProvider) ReadRealizationReport(_ context.Context, scope orders.Scope, period orders.RealizationPeriod) (orders.RealizationReport, error) {
	return p.read(scope, period)
}

type financeTestResolver struct {
	provider orders.Provider
	foreign  bool
	resolved int
}

func (r *financeTestResolver) ResolveOrder(_ context.Context, scope orders.Scope, provider, account string, _ int64) (orders.Binding, error) {
	r.resolved++
	if r.foreign {
		scope.Tenant = "foreign"
	}
	return orders.Binding{Scope: scope, Provider: provider, Account: account, CredentialRevision: 1, Client: r.provider}, nil
}

func TestFinanceReportRequiresCurrentScopedReadPermission(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                      string
		disabled, foreign, revoke bool
		expected                  error
	}{
		{name: "authorized read"},
		{name: "source disabled", disabled: true, expected: orders.ErrBlocked},
		{name: "foreign credential binding", foreign: true, expected: orders.ErrStale},
		{name: "revoked during provider read", revoke: true, expected: orders.ErrBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "posting")
			review.Config.ReadConfigured, review.Config.ReadEnabled = true, !tc.disabled
			store := &flowStore{review: review}
			calls := 0
			provider := financeTestProvider{flowProvider: &flowProvider{}, read: func(scope orders.Scope, period orders.RealizationPeriod) (orders.RealizationReport, error) {
				calls++
				if tc.revoke {
					store.review.Config.ReadEnabled = false
				}
				report := orders.RealizationReport{Scope: scope, Period: period, Number: "synthetic-report", Currency: "RUB"}
				var err error
				report.Fingerprint, err = ordermoney.CommissionSourceFingerprint(report)
				return report, err
			}}
			resolver := &financeTestResolver{provider: provider, foreign: tc.foreign}
			service, err := NewService(store, resolver, ServiceOptions{AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }})
			if err != nil {
				t.Fatal(err)
			}
			report, err := service.RealizationReport(context.Background(), orders.RealizationRequest{Scope: review.Config.Scope, Period: orders.RealizationPeriod{Year: 2026, Month: 9}})
			if !errors.Is(err, tc.expected) {
				t.Fatalf("error = %v, want %v", err, tc.expected)
			}
			if tc.expected == nil && report.Number != "synthetic-report" {
				t.Fatal("authoritative report lost")
			}
			if (tc.disabled || tc.foreign) && calls != 0 {
				t.Fatal("provider read escaped revoked or foreign scope")
			}
		})
	}
}
