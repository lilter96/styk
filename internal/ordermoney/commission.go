package ordermoney

import (
	"encoding/json"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func PlanCommissionDraft(config orders.Config, credentials orders.CredentialRevisions, source orders.RealizationReport, mappings []orders.ProductMappingRef, refs orders.CommissionReferences, id string, now time.Time) (orders.CommissionPlan, []orders.Validation) {
	source.Analysis = nil
	source.Evidence = nil
	plan := orders.CommissionPlan{ID: id, Scope: config.Scope, ConfigRevision: config.Revision, Credentials: credentials, Source: source, References: refs, CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(15 * time.Minute), Lines: []orders.CommissionLine{}}
	if config.Finance == nil || !config.Finance.Active(now, config.Scope, config.Revision) || !refs.Matches(*config.Finance) {
		return plan, commissionIssues("finance_policy_or_references_unverified")
	}
	plan.Policy, plan.PolicyRevision = *config.Finance, config.Finance.Revision
	if issue := commissionSourceValidation(plan, now); issue != "" {
		return plan, commissionIssues(issue)
	}
	if plan.Policy.OtherComponents == orders.CommissionPartialReview || plan.Policy.ReturnTreatment == orders.CommissionReturnsNative {
		return planComponentCommission(plan, mappings)
	}
	gross, err := parseDecimal("0")
	if err != nil {
		return plan, commissionIssues("finance_total_invalid")
	}
	reward := gross
	for _, row := range source.Rows {
		line, invalid := commissionLine(plan, row, mappings)
		if invalid != "" {
			return plan, commissionIssues(invalid)
		}
		if line.Quantity == "0" {
			continue
		}
		quantity, err := parseDecimal(line.Quantity)
		if err != nil {
			return plan, commissionIssues("finance_quantity_invalid")
		}
		price, _, err := monetaryDecimal(line.Price)
		if err != nil {
			return plan, commissionIssues("finance_price_invalid")
		}
		fee, err := parseDecimal(line.Reward.Amount)
		if err != nil {
			return plan, commissionIssues("finance_reward_invalid")
		}
		gross, reward = gross.plus(price.times(quantity)), reward.plus(fee)
		plan.Lines = append(plan.Lines, line)
	}
	return finishCommissionPlan(plan, gross, reward)
}

func finishCommissionPlan(plan orders.CommissionPlan, gross, reward exactDecimal) (orders.CommissionPlan, []orders.Validation) {
	var err error
	plan.ExternalCode, err = orders.CommissionExternalCode(plan.Scope, plan.ID)
	if err != nil {
		return plan, commissionIssues("finance_plan_identity_invalid")
	}
	plan.Totals.Currency = plan.Policy.CurrencyISO
	plan.Totals.Gross, err = gross.decimal()
	if err != nil {
		return plan, commissionIssues("finance_total_range_invalid")
	}
	plan.Totals.Reward, err = reward.decimal()
	if err != nil {
		return plan, commissionIssues("finance_total_range_invalid")
	}
	plan.Totals.Net, err = commissionSubtract(gross, reward).decimal()
	if err != nil {
		return plan, commissionIssues("finance_total_range_invalid")
	}
	plan.Fingerprint, err = orders.CommissionPlanFingerprint(plan)
	if err != nil || plan.Validate() != nil {
		return plan, commissionIssues("finance_plan_invalid")
	}
	return plan, []orders.Validation{}
}

func commissionSubtract(left, right exactDecimal) exactDecimal {
	right.coefficient = right.aligned(right.scale)
	right.coefficient.Neg(right.coefficient)
	return left.plus(right)
}

// Commission rewards/net may be negative. The existing order price conversion
// deliberately rejects negatives, so accounting uses a separately named path.
func CommissionMoneyToMinor(value orders.Money) (json.Number, error) {
	if value.Validate() != nil {
		return "", orders.ErrInvalid
	}
	scale, err := currencyScale(value.Currency)
	if err != nil {
		return "", err
	}
	amount, err := parseDecimal(value.Amount)
	if err != nil {
		return "", err
	}
	text, err := amount.shifted(scale).decimal()
	return json.Number(text), err
}

func CommissionMoneyFromMinor(value json.Number, currency string) (orders.Money, error) {
	scale, err := currencyScale(currency)
	if err != nil {
		return orders.Money{}, err
	}
	amount, err := parseDecimal(orders.Decimal(value))
	if err != nil {
		return orders.Money{}, err
	}
	text, err := amount.shifted(-scale).decimal()
	return orders.Money{Amount: text, Currency: currency}, err
}

func commissionIssues(code string) []orders.Validation {
	return []orders.Validation{{Field: "finance", Code: code}}
}
