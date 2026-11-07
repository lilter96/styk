package ordermoney

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func commissionFixture(t *testing.T) (orders.Config, orders.RealizationReport, []orders.ProductMappingRef, orders.CommissionReferences, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "seller", MoyskladAccount: "account"}
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	policy := &orders.CommissionPolicy{ID: "policy", Scope: scope, Revision: 1, ConfigRevision: 2, OwnerID: "owner", Enabled: true, GrantedAt: now.Add(-time.Hour), OrganizationID: id(1), AgentID: id(2), ContractID: id(3), CurrencyID: id(4), CurrencyISO: "RUB", RateValue: "1", PriceBasis: orders.CommissionSellerPrice, VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), VAT: "0", OtherComponents: orders.CommissionZeroOtherComponents}
	refs := orders.CommissionReferences{Scope: scope, OrganizationID: id(1), AgentID: id(2), ContractID: id(3), CurrencyID: id(4), CurrencyISO: "RUB", ContractType: "Commission", ContractOrganizationID: id(1), ContractAgentID: id(2), ContractCurrencyID: id(4), ContractRewardType: "None", Revision: "proof", ObservedAt: now, Complete: true}
	zero := orders.Present(orders.Decimal("0"))
	returns := orders.RealizationAmounts{Presence: orders.PresenceValue, Quantity: zero, Amount: zero, Bonus: zero, StandardFee: zero, BankCoinvestment: zero, PickUpPointCoinvestment: zero, Stars: zero, Total: zero}
	delivery := returns
	delivery.Quantity, delivery.Amount, delivery.Bonus, delivery.StandardFee, delivery.Total, delivery.PricePerInstance = orders.Present(orders.Decimal("2")), orders.Present(orders.Decimal("200")), orders.Present(orders.Decimal("5")), orders.Present(orders.Decimal("20")), orders.Present(orders.Decimal("185")), orders.Present(orders.Decimal("100"))
	source := orders.RealizationReport{Scope: scope, Period: orders.RealizationPeriod{Year: 2026, Month: 10}, Number: "R-10", Currency: "RUB", DocumentDate: orders.Present("2026-11-05"), StartDate: orders.Present("2026-10-01"), StopDate: orders.Present("2026-10-31"), Rows: []orders.RealizationRow{{Number: 1, SKU: orders.Present("sku"), OfferID: orders.Present("offer"), SellerUnitPrice: orders.Present(orders.Decimal("100")), Delivery: delivery, Return: returns}}, ObservedAt: now}
	mapping := orders.ProductMappingRef{ID: "mapping", Version: 1, Scope: scope, OzonProductID: "product", SKU: "sku", OfferID: "offer", MoyskladKind: "product", MoyskladID: id(5), Confirmed: true, ConfirmedAt: now}
	return orders.Config{Scope: scope, Revision: 2, Finance: policy}, source, []orders.ProductMappingRef{mapping}, refs, now
}

func commissionPlanFixture(t *testing.T, mutate func(*orders.Config, *orders.RealizationReport, *[]orders.ProductMappingRef, *orders.CommissionReferences)) (orders.CommissionPlan, []orders.Validation) {
	t.Helper()
	config, source, mappings, refs, now := commissionFixture(t)
	if mutate != nil {
		mutate(&config, &source, &mappings, &refs)
	}
	var err error
	source.Fingerprint, err = CommissionSourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	return PlanCommissionDraft(config, orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, source, mappings, refs, "preview", now)
}

func TestCommissionDraftExactMoneyAndNegativeReward(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name               string
		bonus, net, reward string
	}{{"normal", "5", "185", "15"}, {"negative reward", "25", "205", "-5"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan, issues := commissionPlanFixture(t, func(_ *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
				s.Rows[0].Delivery.Bonus = orders.Present(orders.Decimal(tc.bonus))
				s.Rows[0].Delivery.Total = orders.Present(orders.Decimal(tc.net))
			})
			if len(issues) != 0 || plan.Validate() != nil || plan.Totals.Gross != "200" || plan.Totals.Reward != orders.Decimal(tc.reward) || plan.Totals.Net != orders.Decimal(tc.net) {
				t.Fatalf("plan=%+v issues=%+v", plan, issues)
			}
			minor, err := CommissionMoneyToMinor(plan.Lines[0].Reward)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := CommissionMoneyFromMinor(minor, "RUB")
			if err != nil || actual != plan.Lines[0].Reward {
				t.Fatalf("reward roundtrip=%+v %v", actual, err)
			}
		})
	}
}

func TestCommissionDraftBlocksUnreviewedAccounting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*orders.Config, *orders.RealizationReport, *[]orders.ProductMappingRef, *orders.CommissionReferences)
		code   string
	}{
		{"returns", func(_ *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			s.Rows[0].Return.Quantity = orders.Present(orders.Decimal("1"))
		}, "finance_returns_require_separate_accounting_review"},
		{"loyalty", func(_ *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			s.Rows[0].Delivery.Stars = orders.Present(orders.Decimal("1"))
		}, "finance_nonzero_or_unknown_components_require_review"},
		{"missing fee", func(_ *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			s.Rows[0].Delivery.StandardFee = orders.Field[orders.Decimal]{Presence: orders.PresenceMissing}
		}, "finance_required_sales_amount_missing"},
		{"price mismatch", func(_ *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			s.Rows[0].SellerUnitPrice = orders.Present(orders.Decimal("101"))
		}, "finance_selected_price_amount_mismatch"},
		{"zero quantity adjustment", func(_ *orders.Config, s *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			s.Rows[0].Delivery.Quantity = orders.Present(orders.Decimal("0"))
		}, "finance_quantity_zero_adjustment_requires_review"},
		{"missing mapping", func(_ *orders.Config, _ *orders.RealizationReport, m *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			*m = nil
		}, "finance_confirmed_product_mapping_required"},
		{"wrong contract", func(_ *orders.Config, _ *orders.RealizationReport, _ *[]orders.ProductMappingRef, r *orders.CommissionReferences) {
			r.ContractType = "Sales"
		}, "finance_policy_or_references_unverified"},
		{"VAT exclusive", func(c *orders.Config, _ *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			c.Finance.VATEnabled = orders.Present(true)
			c.Finance.VATIncluded = orders.Present(false)
			c.Finance.VAT = "20"
		}, "finance_policy_or_references_unverified"},
		{"implicit VAT", func(c *orders.Config, _ *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
			c.Finance.VATEnabled = orders.Field[bool]{}
		}, "finance_policy_or_references_unverified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, issues := commissionPlanFixture(t, tc.mutate)
			if len(issues) != 1 || issues[0].Code != tc.code {
				t.Fatalf("issues=%+v", issues)
			}
		})
	}
}

func TestCommissionReadbackRequiresExactDraftAndAmounts(t *testing.T) {
	t.Parallel()
	plan, issues := commissionPlanFixture(t, nil)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	line := plan.Lines[0]
	actual := orders.CommissionObservation{Revision: "readback", ObservedAt: time.Now().UTC(), Scope: plan.Scope, DocumentID: "document", ExternalCode: plan.ExternalCode, References: plan.References, RateValue: "1", Moment: plan.Moment(), RewardType: "None", PeriodStart: plan.PeriodStart(), PeriodEnd: plan.PeriodEnd(), Gross: orders.Money{Amount: "200", Currency: "RUB"}, Net: orders.Money{Amount: "185", Currency: "RUB"}, Lines: []orders.CommissionObservedLine{{ID: "line", ItemKind: line.Mapping.MoyskladKind, ItemID: line.Mapping.MoyskladID, Quantity: line.Quantity, Price: line.Price, Reward: line.Reward, VAT: "0"}}, Complete: true}
	if !CommissionDraftMatches(plan, actual) {
		t.Fatal("exact draft did not match")
	}
	tests := []struct {
		name   string
		change func(*orders.CommissionObservation)
	}{{"missing revision", func(a *orders.CommissionObservation) { a.Revision = "" }}, {"missing observation time", func(a *orders.CommissionObservation) { a.ObservedAt = time.Time{} }}, {"applied", func(a *orders.CommissionObservation) { a.Applicable = true }}, {"reward", func(a *orders.CommissionObservation) { a.Lines[0].Reward.Amount = "16" }}, {"rate", func(a *orders.CommissionObservation) { a.RateValue = "2" }}, {"date", func(a *orders.CommissionObservation) { a.Moment = "2026-11-06 00:00:00" }}, {"returns", func(a *orders.CommissionObservation) { a.ReturnCount = 1 }}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var changed orders.CommissionObservation
			if err = json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.change(&changed)
			if CommissionDraftMatches(plan, changed) {
				t.Fatal("changed draft matched")
			}
		})
	}
}
