package orderflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func commissionFlowApprove(t *testing.T, service *Service, store *commissionFlowStore, provider *commissionFlowProvider) orders.CommissionWork {
	t.Helper()
	preview, err := service.PreviewCommission(context.Background(), CommissionPreviewRequest{Scope: store.review.Config.Scope, ExpectedRevision: store.review.Config.Revision, Period: provider.report.Period, Actor: "synthetic-owner"})
	if err != nil || len(preview.Validation) != 0 || preview.ID == "" || preview.Plan.Validate() != nil {
		t.Fatal("commission preview invalid", preview.Validation, err)
	}
	work, err := service.ApproveCommission(context.Background(), orders.CommissionApproval{Scope: preview.Plan.Scope, PreviewID: preview.ID, Actor: "synthetic-owner", IdempotencyKey: "synthetic-finance-approval"})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func TestCommissionPreviewFreezesExactClosedReportAndConfirmedMappings(t *testing.T) {
	t.Parallel()
	service, _, store, provider := commissionFlowFixture(t)
	work := commissionFlowApprove(t, service, store, provider)
	if work.Plan.Source.Fingerprint != provider.report.Fingerprint || work.Plan.Totals.Gross != "200" || work.Plan.Totals.Reward != "15" || work.Plan.Totals.Net != "185" || work.Plan.Lines[0].Mapping.ID != store.review.ProductMappings[0].ID {
		t.Fatal("monthly report evidence or exact accounting amounts lost", work.Plan)
	}
	if store.preview.Actor != "synthetic-owner" || store.preview.Automatic || provider.writes != 0 {
		t.Fatal("preview performed a write or borrowed automation", store.preview, provider.writes)
	}
}

func TestCommissionPreviewRequiresIndependentActivePolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*commissionFlowStore)
	}{
		{name: "source permission revoked", change: func(s *commissionFlowStore) { s.review.Config.ReadEnabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, _, store, provider := commissionFlowFixture(t)
			tc.change(store)
			_, err := service.PreviewCommission(context.Background(), CommissionPreviewRequest{Scope: store.review.Config.Scope, ExpectedRevision: store.review.Config.Revision, Period: provider.report.Period, Actor: "synthetic-owner"})
			if !errors.Is(err, orders.ErrBlocked) || provider.reads != 0 || provider.writes != 0 || store.saves != 0 {
				t.Fatal("disabled authority reached provider or durable intent", err, provider.reads, store.saves)
			}
		})
	}
}

func TestCommissionPolicyOffReturnsDurableDiagnosticButCannotApprove(t *testing.T) {
	t.Parallel()
	service, _, store, provider := commissionFlowFixture(t)
	store.review.Config.Finance.Enabled = false
	preview, err := service.PreviewCommission(context.Background(), CommissionPreviewRequest{Scope: store.review.Config.Scope, ExpectedRevision: store.review.Config.Revision, Period: provider.report.Period, Actor: "synthetic-owner"})
	if err != nil || preview.Analysis == nil || preview.Analysis.RowsClassified != 1 || len(preview.Validation) == 0 || provider.reads != 1 || store.saves != 1 || provider.writes != 0 {
		t.Fatal("policy-off diagnostic was withheld or wrote", preview.Validation, err)
	}
	_, err = service.ApproveCommission(context.Background(), orders.CommissionApproval{Scope: preview.Plan.Scope, PreviewID: preview.ID, Actor: "synthetic-owner", IdempotencyKey: "diagnostic-no-write"})
	if !errors.Is(err, orders.ErrBlocked) || store.approved != 0 {
		t.Fatal("diagnostic became writable", err)
	}
}

func TestCommissionScheduleUsesIndependentGrantAndClosedPeriod(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		day       int
		existing  bool
		approvals int
	}{
		{name: "before publication threshold", day: 4},
		{name: "closed month without mirror grant", day: 5, approvals: 1},
		{name: "existing period never reenrolled", day: 5, existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, _, store, provider := commissionFlowFixture(t)
			now := time.Date(2026, 10, tc.day, 9, 0, 0, 0, time.UTC)
			service.options.Now = func() time.Time { return now }
			store.review.Config.Enabled, store.review.Config.TestEnabled, store.review.Config.Automation = false, false, nil
			store.review.Config.Finance.GrantedAt = now.Add(-time.Hour)
			provider.report.ObservedAt, provider.refs.ObservedAt = now, now
			store.existing = tc.existing
			if err := service.ProcessCommissionPeriod(context.Background(), store.review.Config); err != nil {
				t.Fatal(err)
			}
			if store.approved != tc.approvals || store.approved > 0 && (!store.approval.Automatic || store.preview.Plan.Source.Period != (orders.RealizationPeriod{Year: 2026, Month: 9})) {
				t.Fatal("finance scheduler borrowed mirror grant or wrong period", store.approved, store.approval)
			}
		})
	}
}
