package ordermoney

import "github.com/lilter96/styk/internal/orders"

func markCommissionComponents(ledger *orders.FinancialLedger, row int64, side string, line orders.CommissionLine, issue string) {
	for i := range ledger.Components {
		component := &ledger.Components[i]
		if component.SourceRow != row || component.Side != side {
			continue
		}
		if issue != "" {
			if component.Disposition == "UNREPRESENTED" {
				component.Blocker = issue
			}
			continue
		}
		if (component.Component == "legacy_commission" || component.Component == "compensation") && !component.Value.IsKnown() {
			component.Disposition = "NOT_APPLICABLE"
			component.Blocker = ""
			continue
		}
		if component.Disposition == "ZERO" {
			continue
		}
		switch component.Component {
		case "quantity", "amount", "price_per_instance":
			component.Disposition = "NATIVE_" + side
			component.Blocker = ""
		case "standard_fee", "bonus":
			component.Disposition = "NATIVE_REWARD"
			component.Blocker = ""
		case "total":
			markCommissionControlTotal(component, line)
		default:
			if component.Disposition == "UNREPRESENTED" {
				component.Blocker = "finance_component_retained_for_review"
			}
		}
	}
}

func markCommissionControlTotal(component *orders.FinancialComponent, line orders.CommissionLine) {
	for _, entry := range []orders.Field[orders.Decimal]{component.Value} {
		if !entry.IsKnown() {
			return
		}
		if line.Quantity == "0" {
			if orders.ExactDecimalEqual(*entry.Value, "0") {
				component.Disposition = "ZERO"
				component.Blocker = ""
			}
			return
		}
		totals, err := commissionLineTotals([]orders.CommissionLine{line}, line.Price.Currency)
		if err != nil {
			component.Blocker = "finance_control_total_unverified"
			return
		}
		if orders.ExactDecimalEqual(totals.Net, *entry.Value) {
			component.Disposition = "NATIVE_" + component.Side
			component.Blocker = ""
			return
		}
		component.Disposition, component.Blocker = "UNREPRESENTED", "finance_control_total_includes_unrepresented_components"
	}
}

func markCommissionLedgerRows(ledger *orders.FinancialLedger, plan orders.CommissionPlan) {
	for i := range ledger.Rows {
		row := &ledger.Rows[i]
		mapped := false
		unrepresented := false
		for _, line := range append(append([]orders.CommissionLine{}, plan.Lines...), plan.Returns...) {
			mapped = mapped || line.SourceRow == row.SourceRow
		}
		for _, component := range ledger.Components {
			if component.SourceRow != row.SourceRow {
				continue
			}
			if component.Disposition == "UNREPRESENTED" || component.Disposition == "UNRECOGNIZED" {
				unrepresented = true
				if component.Blocker != "" {
					row.Blockers = appendDistinctFinancialBlocker(row.Blockers, component.Blocker)
				}
			}
		}
		row.NativeStatus = "UNMAPPED"
		if mapped {
			row.NativeStatus = "MAPPED"
			if unrepresented {
				row.NativeStatus = "PARTIAL"
			}
		}
		if !mapped && !unrepresented && row.Classification == "ZERO" {
			row.NativeStatus = "ZERO"
		}
	}
}

func appendDistinctFinancialBlocker(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
