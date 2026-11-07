package ordermoney

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestFinancialLedgerClassifiesEveryComponentWithoutInventingNullZero(t *testing.T) {
	t.Parallel()
	_, source, _, _, _ := commissionFixture(t)
	source.Rows[0].Return = orders.RealizationAmounts{Presence: orders.PresenceNull}
	source.Rows[0].Delivery.BankCoinvestment = orders.Present(orders.Decimal("2"))
	source.Rows[0].Delivery.Total = orders.Present(orders.Decimal("187"))
	var err error
	source.Fingerprint, err = CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := ClassifyRealization(source)
	if err != nil || ledger.RowsClassified != 1 || ledger.RowsMappedToNative != 0 || ledger.RowsUnmapped != 1 || ledger.ComponentsTotal != 24 || ledger.Complete {
		t.Fatalf("invalid policy-free ledger: %+v %v", ledger, err)
	}
	if ledger.ComponentsRepresented+ledger.ComponentsUnrepresented != ledger.ComponentsTotal {
		t.Fatal("component conservation failed")
	}
	for _, component := range ledger.Components {
		if component.Side == "RETURN" && component.Value.IsKnown() {
			t.Fatal("NULL return group became known zero")
		}
	}
	for _, reconciliation := range ledger.Reconciliation {
		if reconciliation.Side == "SALE" && (!reconciliation.Difference.IsKnown() || *reconciliation.Difference.Value != "-2" || !reconciliation.ComponentDifference.IsKnown() || *reconciliation.ComponentDifference.Value != "0") {
			t.Fatal("bank residual or source identity lost", reconciliation)
		}
	}
}

func TestCommissionPartialDraftRetainsBankAndUnknownReturnResidual(t *testing.T) {
	t.Parallel()
	plan, issues := commissionPlanFixture(t, func(c *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
		c.Finance.OtherComponents = orders.CommissionPartialReview
		s.Rows[0].Return = orders.RealizationAmounts{Presence: orders.PresenceNull}
		s.Rows[0].Delivery.BankCoinvestment = orders.Present(orders.Decimal("2"))
		s.Rows[0].Delivery.Total = orders.Present(orders.Decimal("187"))
	})
	if len(issues) != 0 || plan.Validate() != nil || plan.Accounting == nil || plan.Accounting.Complete || plan.Accounting.RowsPartial != 1 || plan.Totals.Net != "185" || !CommissionPlanComponentsSupported(plan) {
		t.Fatalf("partial source silently changed native amount or failed: issues=%+v accounting=%+v totals=%+v", issues, plan.Accounting, plan.Totals)
	}
	if plan.Lines[0].Reward.Amount != "15" {
		t.Fatal("bank subsidy was folded into commissioner reward")
	}
}

func TestCommissionNativeReturnsAreAccountingLinesWithSignedRewards(t *testing.T) {
	t.Parallel()
	plan, issues := commissionPlanFixture(t, func(c *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
		c.Finance.ReturnTreatment = orders.CommissionReturnsNative
		a := s.Rows[0].Delivery
		a.Quantity = orders.Present(orders.Decimal("1"))
		a.Amount = orders.Present(orders.Decimal("100"))
		a.Bonus = orders.Present(orders.Decimal("25"))
		a.StandardFee = orders.Present(orders.Decimal("10"))
		a.Total = orders.Present(orders.Decimal("115"))
		s.Rows[0].Return = a
	})
	if len(issues) != 0 || plan.Validate() != nil || len(plan.Returns) != 1 || plan.Returns[0].Reward.Amount != "-15" || plan.ReturnTotals.Net != "115" || plan.Totals.Gross != "100" || plan.Totals.Reward != "30" || plan.Totals.Net != "70" || !plan.Accounting.Complete {
		t.Fatalf("return accounting is inexact: issues=%+v plan=%+v", issues, plan)
	}
}

func TestCommissionPartialCannotCreateEmptyOrFakeAdjustmentPositions(t *testing.T) {
	t.Parallel()
	for _, quantity := range []orders.Decimal{"0", "-1"} {
		t.Run(string(quantity), func(t *testing.T) {
			t.Parallel()
			_, issues := commissionPlanFixture(t, func(c *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
				c.Finance.OtherComponents = orders.CommissionPartialReview
				s.Rows[0].Delivery.Quantity = orders.Present(quantity)
			})
			if len(issues) == 0 {
				t.Fatal("monetary adjustment became fake product position")
			}
		})
	}
}

func TestFinancialLedgerObservedShapeCoverageIsNotNativeSuccess(t *testing.T) {
	t.Parallel()
	_, source, _, _, _ := commissionFixture(t)
	sale := source.Rows[0]
	sale.Return = orders.RealizationAmounts{Presence: orders.PresenceNull}
	source.Rows = make([]orders.RealizationRow, 0, 254)
	for i := 0; i < 254; i++ {
		row := sale
		row.Number = int64(i + 1)
		if i == 252 {
			row.Return = row.Delivery
		}
		if i == 253 {
			row.Return = row.Delivery
			row.Delivery = orders.RealizationAmounts{Presence: orders.PresenceNull}
		}
		source.Rows = append(source.Rows, row)
	}
	var err error
	source.Fingerprint, err = CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := ClassifyRealization(source)
	if err != nil {
		t.Fatal(err)
	}
	classes := make(map[string]int)
	for _, row := range ledger.Rows {
		classes[row.Classification]++
	}
	if ledger.RowsClassified != 254 || ledger.ComponentsTotal != 6096 || classes["SALE"] != 252 || classes["MIXED"] != 1 || classes["RETURN"] != 1 || ledger.RowsMappedToNative != 0 || ledger.RowsUnmapped != 254 || ledger.Complete {
		t.Fatal("shape coverage was misreported", classes, ledger.RowsClassified)
	}
}

func TestCommissionStrictReturnPolicyStillBlocksBankOrNullResidual(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"bank", "null-return", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			plan, issues := commissionPlanFixture(t, func(c *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
				c.Finance.ReturnTreatment = orders.CommissionReturnsNative
				switch kind {
				case "bank":
					s.Rows[0].Delivery.BankCoinvestment = orders.Present(orders.Decimal("1"))
				case "null-return":
					s.Rows[0].Return = orders.RealizationAmounts{Presence: orders.PresenceNull}
				case "unknown":
					s.UnknownComponents = []orders.RealizationUnknownComponent{{SourceRow: 1, Path: "delivery_commission.future", Value: orders.Present(orders.Decimal("2")), ValueDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
				}
			})
			if len(issues) == 0 || plan.Accounting == nil || plan.Accounting.Complete {
				t.Fatal("strict mode silently accepted retained residual")
			}
		})
	}
}
