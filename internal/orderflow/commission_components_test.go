package orderflow

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func TestCommissionRunnerPartialDraftRequiresAccountingReviewAfterReadback(t *testing.T) {
	t.Parallel()
	service, runner, store, provider := commissionFlowFixture(t)
	store.review.Config.Finance.OtherComponents = orders.CommissionPartialReview
	provider.report.Rows[0].Delivery.BankCoinvestment = orders.Present(orders.Decimal("2"))
	provider.report.Rows[0].Delivery.Total = orders.Present(orders.Decimal("187"))
	var err error
	provider.report.Fingerprint, err = ordermoney.CommissionSourceFingerprint(provider.report)
	if err != nil {
		t.Fatal(err)
	}
	work := commissionFlowApprove(t, service, store, provider)
	if work.Plan.Accounting == nil || work.Plan.Accounting.Complete || work.Plan.Totals.Net != "185" {
		t.Fatal("partial report lost residual or invented payout")
	}
	for i := 0; i < 2; i++ {
		if _, err = runner.RunNext(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if provider.writes != 1 || store.financeWork.State != orders.StateReconciled || store.financeWork.Code != "FINANCE_DRAFT_CONFIRMED_NEEDS_ACCOUNTING_REVIEW" {
		t.Fatal("partial draft was called financially complete", store.financeWork.Code)
	}
}

func TestCommissionRunnerPostedExternallyNeedsExactCurrentIdentity(t *testing.T) {
	t.Parallel()
	for _, foreign := range []bool{false, true} {
		name := "exact"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service, runner, store, provider := commissionFlowFixture(t)
			commissionFlowApprove(t, service, store, provider)
			if _, err := runner.RunNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			provider.actual.Applicable = true
			if foreign {
				provider.actual.DocumentID = "foreign-document"
			}
			if _, err := runner.RunNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "FINANCE_DOCUMENT_POSTED_EXTERNALLY"
			if foreign {
				want = "FINANCE_DOCUMENT_CONTENT_CONFLICT"
			}
			if store.financeWork.State != orders.StateUnknown || store.financeWork.Code != want || provider.writes != 1 {
				t.Fatal("posted document was overwritten, adopted or falsely attributed", store.financeWork.Code)
			}
		})
	}
}
