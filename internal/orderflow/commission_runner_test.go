package orderflow

import (
	"context"
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func TestCommissionRunnerLostAcknowledgementIsReadbackOnly(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	commissionFlowApprove(t, service, store, provider)
	provider.lost = true
	if worked, err := runner.RunNext(context.Background()); !worked || err != nil || provider.writes != 1 || store.financeWork.State != orders.StateUnknown {
		t.Fatal("lost write not durable UNKNOWN", worked, err, provider.writes, store.financeWork.State)
	}
	if worked, err := runner.RunNext(context.Background()); !worked || err != nil || store.financeWork.State != orders.StateReconciled {
		t.Fatal("readback failed", worked, err, store.financeWork.State)
	}
	if worked, err := runner.RunNext(context.Background()); worked || err != nil || provider.writes != 1 {
		t.Fatal("replay duplicated monthly report", worked, err, provider.writes)
	}
	if len(store.completions) != 2 || store.completions[1].Observation == nil {
		t.Fatal("reconciled without exact native document evidence")
	}
}

func TestCommissionRunnerUnknownNotFoundNeverResends(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	commissionFlowApprove(t, service, store, provider)
	provider.lost = true
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	provider.actual = nil
	for range 3 {
		if _, err := runner.RunNext(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if provider.writes != 1 || store.financeWork.State != orders.StateUnknown || store.financeWork.Code != "FINANCE_DOCUMENT_NOT_YET_OBSERVED" {
		t.Fatal("unobserved write was retried or treated as absent", provider.writes, store.financeWork)
	}
}

func TestCommissionRunnerDoesNotAdoptPreexistingDraftWithoutOwnAttempt(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	work := commissionFlowApprove(t, service, store, provider)
	actual := commissionFlowObservation(work.Plan)
	provider.actual = &actual
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 0 || store.financeWork.DispatchCount != 0 || store.financeWork.State != orders.StateBlocked || store.financeWork.Code != "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW" {
		t.Fatal("unowned native report adopted as own outcome", provider.writes, store.financeWork)
	}
}

func TestCommissionRunnerSourceAndReferencesChangeBlockBeforeWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*commissionFlowProvider)
	}{
		{name: "source correction", change: func(p *commissionFlowProvider) { p.report.Fingerprint = "corrected-report-fingerprint" }},
		{name: "native references changed", change: func(p *commissionFlowProvider) { p.refs.Revision = "changed-reference-version" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, runner, store, provider := commissionFlowFixture(t)
			commissionFlowApprove(t, service, store, provider)
			tc.change(provider)
			if _, err := runner.RunNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if provider.writes != 0 || store.financeWork.DispatchCount != 0 || store.financeWork.State != orders.StateBlocked {
				t.Fatal("changed accounting facts reached POST", store.financeWork, provider.writes)
			}
		})
	}
}

func TestCommissionRunnerRechecksAuthorityAtFinalAdmission(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	commissionFlowApprove(t, service, store, provider)
	provider.beforeAuthorize = func() { store.guardDenied = true }
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 0 || store.financeWork.DispatchCount != 0 || store.financeWork.Code != "FINANCE_FINAL_ADMISSION_BLOCKED" {
		t.Fatal("revoked owner consent reached POST", store.financeWork, provider.writes)
	}
}

func TestCommissionRunnerReportsCorrectionWithoutChangingNativeDraft(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	commissionFlowApprove(t, service, store, provider)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	provider.report.Rows = slices.Clone(provider.report.Rows)
	provider.report.Rows[0].Delivery.Total = orders.Present(orders.Decimal("180"))
	var err error
	provider.report.Fingerprint, err = ordermoney.CommissionSourceFingerprint(provider.report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 || !store.financeWork.SourceChanged || store.financeWork.State != orders.StateReconciled || store.financeWork.Code != "FINANCE_SOURCE_CORRECTION_REQUIRES_REVIEW" {
		t.Fatal("correction overwrote or concealed original draft", store.financeWork, provider.writes)
	}
}

func TestCommissionRunnerReadbackRequiresCurrentReadAuthority(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	commissionFlowApprove(t, service, store, provider)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	reads := provider.nativeReads
	store.guardDenied = true
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.nativeReads != reads || provider.writes != 1 || store.financeWork.Code != "FINANCE_READBACK_AUTHORITY_CHANGED" {
		t.Fatal("revoked read authority escaped DB guard", store.financeWork)
	}
}

func TestCommissionRunnerFinanceOptOutStillAllowsOwnOutcomeReadback(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	commissionFlowApprove(t, service, store, provider)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.review.Config.Finance.Enabled = false
	store.review.Config.Revision++
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 || store.financeWork.State != orders.StateReconciled {
		t.Fatal("own uncertain outcome depended on new-write consent", store.financeWork)
	}
}
