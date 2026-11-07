package ordermoney

import (
	"github.com/lilter96/styk/internal/orders"
)

func CommissionDraftMatches(plan orders.CommissionPlan, actual orders.CommissionObservation) bool {
	policy := plan.Policy
	if plan.Validate() != nil || !actual.Complete || actual.Revision == "" || actual.ObservedAt.IsZero() || actual.Scope != plan.Scope || actual.DocumentID == "" || actual.ExternalCode != plan.ExternalCode || actual.Applicable || !actual.References.Matches(policy) || actual.References.Revision != plan.References.Revision || actual.RewardType != "None" || actual.ReturnCount != len(plan.Returns) || actual.VATEnabled != *policy.VATEnabled.Value || actual.VATIncluded != *policy.VATIncluded.Value {
		return false
	}
	if !commissionPeriodEqual(actual.PeriodStart, plan.PeriodStart()) || !commissionPeriodEqual(actual.PeriodEnd, plan.PeriodEnd()) || !commissionPeriodEqual(actual.Moment, plan.Moment()) || !orders.ExactDecimalEqual(actual.RateValue, policy.RateValue) || len(actual.Lines) != len(plan.Lines) {
		return false
	}
	if actual.Gross.Currency != plan.Totals.Currency || actual.Net.Currency != plan.Totals.Currency || !orders.ExactDecimalEqual(actual.Gross.Amount, plan.Totals.Gross) || !orders.ExactDecimalEqual(actual.Net.Amount, plan.Totals.Net) {
		return false
	}
	return commissionObservedLinesMatch(plan.Lines, actual.Lines, policy) && commissionObservedLinesMatch(plan.Returns, actual.Returns, policy)
}

func commissionObservedLinesMatch(wantedLines []orders.CommissionLine, actualLines []orders.CommissionObservedLine, policy orders.CommissionPolicy) bool {
	if len(wantedLines) != len(actualLines) {
		return false
	}
	used := make([]bool, len(actualLines))
	for _, wanted := range wantedLines {
		found := false
		for index, line := range actualLines {
			if used[index] || !commissionObservedLineMatches(wanted, line, policy) {
				continue
			}
			used[index], found = true, true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func commissionObservedLineMatches(wanted orders.CommissionLine, actual orders.CommissionObservedLine, policy orders.CommissionPolicy) bool {
	return actual.ID != "" && actual.ItemID == wanted.Mapping.MoyskladID && actual.ItemKind == wanted.Mapping.MoyskladKind && orders.ExactDecimalEqual(actual.Quantity, wanted.Quantity) && actual.Price.Currency == wanted.Price.Currency && orders.ExactDecimalEqual(actual.Price.Amount, wanted.Price.Amount) && actual.Reward.Currency == wanted.Reward.Currency && orders.ExactDecimalEqual(actual.Reward.Amount, wanted.Reward.Amount) && orders.ExactDecimalEqual(actual.VAT, policy.VAT) && actual.VATEnabled == *policy.VATEnabled.Value
}

func commissionPeriodEqual(left, right string) bool {
	a, err := orders.NativeParentVersionTime(left)
	if err != nil {
		return false
	}
	b, err := orders.NativeParentVersionTime(right)
	return err == nil && a.Equal(b)
}
