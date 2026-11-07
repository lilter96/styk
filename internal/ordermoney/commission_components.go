package ordermoney

import (
	"strconv"

	"github.com/lilter96/styk/internal/orders"
)

func planComponentCommission(plan orders.CommissionPlan, mappings []orders.ProductMappingRef) (orders.CommissionPlan, []orders.Validation) {
	ledger, err := ClassifyRealization(plan.Source)
	if err != nil {
		return plan, commissionIssues("finance_component_ledger_invalid")
	}
	plan.Accounting = &ledger
	for _, row := range plan.Source.Rows {
		for _, side := range []struct {
			name    string
			amounts orders.RealizationAmounts
		}{{"SALE", row.Delivery}, {"RETURN", row.Return}} {
			line, issue := commissionComponentLine(plan, row, side.name, side.amounts, mappings)
			markCommissionComponents(&ledger, row.Number, side.name, line, issue)
			if issue != "" || line.Quantity == "0" {
				continue
			}
			if side.name == "SALE" {
				plan.Lines = append(plan.Lines, line)
			} else {
				plan.Returns = append(plan.Returns, line)
			}
		}
	}
	markCommissionLedgerRows(&ledger, plan)
	ledger, err = finishFinancialLedger(ledger)
	if err != nil {
		return plan, commissionIssues("finance_component_totals_invalid")
	}
	plan.Accounting = &ledger
	if plan.Policy.OtherComponents != orders.CommissionPartialReview && !ledger.Complete {
		return plan, commissionIssues("finance_unrepresented_components_require_review")
	}
	if len(plan.Lines)+len(plan.Returns) == 0 {
		return plan, commissionIssues("finance_no_supported_native_positions")
	}
	return finishComponentCommission(plan)
}

func commissionComponentLine(plan orders.CommissionPlan, row orders.RealizationRow, side string, amounts orders.RealizationAmounts, mappings []orders.ProductMappingRef) (orders.CommissionLine, string) {
	line := orders.CommissionLine{SourceRow: row.Number, Quantity: "0"}
	for _, value := range []orders.Field[orders.Decimal]{amounts.Quantity, amounts.Amount, amounts.StandardFee, amounts.Bonus} {
		if !value.IsKnown() {
			return line, "finance_required_component_value_unknown"
		}
	}
	if orders.ExactDecimalEqual(*amounts.Quantity.Value, "0") {
		for _, value := range []orders.Field[orders.Decimal]{amounts.Amount, amounts.StandardFee, amounts.Bonus} {
			if !orders.ExactDecimalEqual(*value.Value, "0") {
				return line, "finance_quantity_zero_adjustment_requires_review"
			}
		}
		return line, ""
	}
	if side == "RETURN" && plan.Policy.ReturnTreatment != orders.CommissionReturnsNative {
		return line, "finance_return_treatment_requires_review"
	}
	quantity, err := strconv.ParseInt(string(*amounts.Quantity.Value), 10, 32)
	if err != nil || quantity < 1 {
		return line, "finance_negative_quantity_correction_requires_review"
	}
	row.Delivery = amounts
	line, issue := commissionMappedLine(plan, row, mappings)
	if issue != "" {
		return line, issue
	}
	fee, err := parseDecimal(*amounts.StandardFee.Value)
	if err != nil {
		return line, "finance_reward_invalid"
	}
	bonus, err := parseDecimal(*amounts.Bonus.Value)
	if err != nil {
		return line, "finance_reward_invalid"
	}
	line.Reward.Amount, err = commissionSubtract(fee, bonus).decimal()
	line.Reward.Currency = plan.Policy.CurrencyISO
	if err != nil {
		return line, "finance_reward_invalid"
	}
	return line, commissionComponentAmounts(line, amounts)
}

func commissionComponentAmounts(line orders.CommissionLine, amounts orders.RealizationAmounts) string {
	price, scale, err := monetaryDecimal(line.Price)
	if err != nil {
		return "finance_selected_price_invalid"
	}
	quantity, err := parseDecimal(line.Quantity)
	if err != nil {
		return "finance_quantity_invalid"
	}
	reward, err := parseDecimal(line.Reward.Amount)
	if err != nil {
		return "finance_reward_invalid"
	}
	gross := price.times(quantity)
	if !gross.rounded(scale).equal(gross) || !reward.rounded(scale).equal(reward) {
		return "finance_rounding_requires_review"
	}
	amount, err := parseDecimal(*amounts.Amount.Value)
	if err != nil || !gross.equal(amount) {
		return "finance_selected_price_amount_mismatch"
	}
	return ""
}

func finishComponentCommission(plan orders.CommissionPlan) (orders.CommissionPlan, []orders.Validation) {
	sales, err := commissionLineTotals(plan.Lines, plan.Policy.CurrencyISO)
	if err != nil {
		return plan, commissionIssues("finance_total_invalid")
	}
	returned, err := commissionLineTotals(plan.Returns, plan.Policy.CurrencyISO)
	if err != nil {
		return plan, commissionIssues("finance_total_invalid")
	}
	if len(plan.Returns) > 0 {
		plan.ReturnTotals = &returned
	}
	gross, err := commissionTotalDifference(sales.Gross, returned.Gross)
	if err != nil {
		return plan, commissionIssues("finance_total_invalid")
	}
	reward, err := commissionTotalDifference(sales.Reward, returned.Reward)
	if err != nil {
		return plan, commissionIssues("finance_total_invalid")
	}
	return finishCommissionPlan(plan, gross, reward)
}

func commissionTotalDifference(left, right orders.Decimal) (exactDecimal, error) {
	a, err := parseDecimal(left)
	if err != nil {
		return exactDecimal{}, err
	}
	b, err := parseDecimal(right)
	if err != nil {
		return exactDecimal{}, err
	}
	return commissionSubtract(a, b), nil
}

func commissionLineTotals(lines []orders.CommissionLine, currency string) (orders.CommissionTotals, error) {
	gross, err := parseDecimal("0")
	if err != nil {
		return orders.CommissionTotals{}, err
	}
	reward := gross
	for _, line := range lines {
		price, _, err := monetaryDecimal(line.Price)
		if err != nil {
			return orders.CommissionTotals{}, err
		}
		quantity, err := parseDecimal(line.Quantity)
		if err != nil {
			return orders.CommissionTotals{}, err
		}
		fee, err := parseDecimal(line.Reward.Amount)
		if err != nil {
			return orders.CommissionTotals{}, err
		}
		gross, reward = gross.plus(price.times(quantity)), reward.plus(fee)
	}
	result := orders.CommissionTotals{Currency: currency}
	for _, item := range []struct {
		value  exactDecimal
		target *orders.Decimal
	}{{gross, &result.Gross}, {reward, &result.Reward}, {commissionSubtract(gross, reward), &result.Net}} {
		*item.target, err = item.value.decimal()
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
