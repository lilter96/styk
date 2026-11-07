package ordermoney

import "github.com/lilter96/styk/internal/orders"

func financialRowComponents(row orders.RealizationRow) []orders.FinancialComponent {
	components := []orders.FinancialComponent{{Side: "ROW", Component: "seller_unit_price", Category: "SELLER_PRICE", Role: "UNIT_PRICE", Value: row.SellerUnitPrice}, {Side: "ROW", Component: "commission_ratio", Category: "COMMISSION_RATIO", Role: "PERCENT", Value: row.CommissionRatio}}
	components = append(components, financialAmountComponents("SALE", row.Delivery)...)
	return append(components, financialAmountComponents("RETURN", row.Return)...)
}

func financialAmountComponents(side string, amounts orders.RealizationAmounts) []orders.FinancialComponent {
	components := []orders.FinancialComponent{
		{Side: side, Component: "quantity", Category: "QUANTITY", Role: "QUANTITY", Value: amounts.Quantity},
		{Side: side, Component: "amount", Category: side + "_GROSS", Role: "BASE", Value: amounts.Amount},
		{Side: side, Component: "bonus", Category: "DISCOUNT_POINTS", Role: "ADJUSTMENT", Value: amounts.Bonus},
		{Side: side, Component: "legacy_commission", Category: "LEGACY_COMMISSION", Role: "ADJUSTMENT", Value: amounts.LegacyCommission},
		{Side: side, Component: "compensation", Category: "COMPENSATION", Role: "ADJUSTMENT", Value: amounts.Compensation},
		{Side: side, Component: "price_per_instance", Category: "SALE_PRICE", Role: "UNIT_PRICE", Value: amounts.PricePerInstance},
		{Side: side, Component: "standard_fee", Category: "COMMISSION_FEE", Role: "ADJUSTMENT", Value: amounts.StandardFee},
		{Side: side, Component: "bank_coinvestment", Category: "BANK_COINVESTMENT", Role: "ADJUSTMENT", Value: amounts.BankCoinvestment},
		{Side: side, Component: "pick_up_point_coinvestment", Category: "PICKUP_COINVESTMENT", Role: "ADJUSTMENT", Value: amounts.PickUpPointCoinvestment},
		{Side: side, Component: "stars", Category: "LOYALTY", Role: "ADJUSTMENT", Value: amounts.Stars},
		{Side: side, Component: "total", Category: "CONTROL_TOTAL", Role: "CONTROL_TOTAL", Value: amounts.Total},
	}
	for i := range components {
		components[i].SourcePresence = amounts.Presence
	}
	return components
}

func financialReconciliation(row int64, side string, amounts orders.RealizationAmounts) (orders.FinancialReconciliation, error) {
	result := orders.FinancialReconciliation{SourceRow: row, Side: side, Gross: amounts.Amount, SourceNet: amounts.Total, Status: "UNKNOWN", ComponentStatus: "UNKNOWN"}
	if !amounts.Amount.IsKnown() || !amounts.StandardFee.IsKnown() || !amounts.Bonus.IsKnown() || !amounts.Total.IsKnown() {
		return result, nil
	}
	values := make([]exactDecimal, 0, 4)
	for _, field := range []orders.Field[orders.Decimal]{amounts.Amount, amounts.StandardFee, amounts.Bonus, amounts.Total} {
		value, err := parseDecimal(*field.Value)
		if err != nil {
			return result, err
		}
		values = append(values, value)
	}
	reward := commissionSubtract(values[1], values[2])
	expected := commissionSubtract(values[0], reward)
	difference := commissionSubtract(expected, values[3])
	for _, item := range []struct {
		value  exactDecimal
		target *orders.Field[orders.Decimal]
	}{{reward, &result.Reward}, {expected, &result.ExpectedNet}, {difference, &result.Difference}} {
		value, err := item.value.decimal()
		if err != nil {
			return result, err
		}
		*item.target = orders.Present(value)
	}
	result.Status = "DIFFERENCE"
	if difference.coefficient.Sign() == 0 {
		result.Status = "MATCH"
	}
	return financialComponentReconciliation(result, amounts, expected, values[3])
}

func financialComponentReconciliation(result orders.FinancialReconciliation, amounts orders.RealizationAmounts, native, source exactDecimal) (orders.FinancialReconciliation, error) {
	if !amounts.BankCoinvestment.IsKnown() {
		return result, nil
	}
	for _, value := range []orders.Field[orders.Decimal]{amounts.PickUpPointCoinvestment, amounts.Stars} {
		if !value.IsKnown() || !orders.ExactDecimalEqual(*value.Value, "0") {
			return result, nil
		}
	}
	for _, value := range []orders.Field[orders.Decimal]{amounts.LegacyCommission, amounts.Compensation} {
		if value.IsKnown() && !orders.ExactDecimalEqual(*value.Value, "0") {
			return result, nil
		}
	}
	bank, err := parseDecimal(*amounts.BankCoinvestment.Value)
	if err != nil {
		return result, err
	}
	expected := native.plus(bank)
	difference := commissionSubtract(expected, source)
	for _, item := range []struct {
		value  exactDecimal
		target *orders.Field[orders.Decimal]
	}{{expected, &result.ComponentExpectedNet}, {difference, &result.ComponentDifference}} {
		value, err := item.value.decimal()
		if err != nil {
			return result, err
		}
		*item.target = orders.Present(value)
	}
	result.ComponentFormula = "amount-standard_fee+bonus+bank_coinvestment"
	result.ComponentStatus = "DIFFERENCE"
	if difference.coefficient.Sign() == 0 {
		result.ComponentStatus = "MATCH"
	}
	return result, nil
}
