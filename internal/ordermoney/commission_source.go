package ordermoney

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func commissionSourceValidation(plan orders.CommissionPlan, now time.Time) string {
	source := plan.Source
	if source.Period.Validate(now) != nil || source.Period.Year < 2024 || source.Period.Year == 2024 && source.Period.Month < 5 {
		return "finance_legacy_or_open_period_unsupported"
	}
	if source.Scope != plan.Scope || source.Currency != plan.Policy.CurrencyISO || source.ObservedAt.IsZero() || now.Before(source.ObservedAt) || now.Sub(source.ObservedAt) > 15*time.Minute || len(source.Rows) == 0 || len(source.Rows) > orders.MaxCommissionLines {
		return "finance_source_incomplete_or_stale"
	}
	if !source.StartDate.IsKnown() || !source.StopDate.IsKnown() || plan.Moment() == "" {
		return "finance_report_period_unverified"
	}
	start := time.Date(source.Period.Year, time.Month(source.Period.Month), 1, 0, 0, 0, 0, time.UTC)
	if *source.StartDate.Value != start.Format("2006-01-02") || *source.StopDate.Value != start.AddDate(0, 1, -1).Format("2006-01-02") {
		return "finance_report_period_unverified"
	}
	fingerprint, err := CommissionSourceFingerprint(source)
	if err != nil || source.Fingerprint != fingerprint {
		return "finance_source_snapshot_changed"
	}
	if plan.References.ObservedAt.After(now) || now.Sub(plan.References.ObservedAt) > 5*time.Minute {
		return "finance_references_stale"
	}
	return ""
}

func CommissionSourceFingerprint(source orders.RealizationReport) (string, error) {
	source.Analysis = nil
	source.Fingerprint, source.ObservedAt, source.Blockers = "", time.Time{}, nil
	raw, err := json.Marshal(source)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// CommissionSourceComponentsSupported rechecks the current accounting gate for
// an old frozen unsent plan without rewriting its historical source/hash.
func CommissionSourceComponentsSupported(source orders.RealizationReport) bool {
	if len(source.Rows) == 0 || len(source.UnknownComponents) != 0 {
		return false
	}
	for _, row := range source.Rows {
		if commissionAmountsValidation(row) != "" {
			return false
		}
	}
	return true
}

func commissionLine(plan orders.CommissionPlan, row orders.RealizationRow, mappings []orders.ProductMappingRef) (orders.CommissionLine, string) {
	line := orders.CommissionLine{SourceRow: row.Number, Quantity: "0"}
	if issue := commissionAmountsValidation(row); issue != "" {
		return line, issue
	}
	if orders.ExactDecimalEqual(*row.Delivery.Quantity.Value, "0") {
		for _, value := range []orders.Field[orders.Decimal]{row.Delivery.Amount, row.Delivery.Bonus, row.Delivery.StandardFee, row.Delivery.Total} {
			if !orders.ExactDecimalEqual(*value.Value, "0") {
				return line, "finance_quantity_zero_adjustment_requires_review"
			}
		}
		return line, ""
	}
	var issue string
	line, issue = commissionMappedLine(plan, row, mappings)
	if issue != "" {
		return line, issue
	}
	fee, err := parseDecimal(*row.Delivery.StandardFee.Value)
	if err != nil {
		return line, "finance_reward_invalid"
	}
	bonus, err := parseDecimal(*row.Delivery.Bonus.Value)
	if err != nil {
		return line, "finance_reward_invalid"
	}
	line.Reward.Amount, err = commissionSubtract(fee, bonus).decimal()
	line.Reward.Currency = plan.Policy.CurrencyISO
	if err != nil {
		return line, "finance_reward_invalid"
	}
	return line, commissionLineTotal(line, row.Delivery)
}

func commissionMappedLine(plan orders.CommissionPlan, row orders.RealizationRow, mappings []orders.ProductMappingRef) (orders.CommissionLine, string) {
	line := orders.CommissionLine{SourceRow: row.Number}
	quantity, err := strconv.ParseInt(string(*row.Delivery.Quantity.Value), 10, 32)
	if err != nil || quantity < 1 {
		return line, "finance_quantity_invalid"
	}
	line.Quantity = *row.Delivery.Quantity.Value
	for _, mapping := range mappings {
		if mapping.Scope != plan.Scope || mapping.Validate() != nil || !row.SKU.IsKnown() || !row.OfferID.IsKnown() || mapping.SKU != *row.SKU.Value || mapping.OfferID != *row.OfferID.Value {
			continue
		}
		if line.Mapping.ID != "" {
			return line, "finance_product_mapping_ambiguous"
		}
		line.Mapping = mapping
	}
	if line.Mapping.ID == "" {
		return line, "finance_confirmed_product_mapping_required"
	}
	price := row.Delivery.PricePerInstance
	if plan.Policy.PriceBasis == orders.CommissionSellerPrice {
		price = row.SellerUnitPrice
	}
	if !price.IsKnown() {
		return line, "finance_selected_price_missing"
	}
	line.Price = orders.Money{Amount: *price.Value, Currency: plan.Policy.CurrencyISO}
	return line, ""
}

func commissionAmountsValidation(row orders.RealizationRow) string {
	if !row.Delivery.Quantity.IsKnown() || !row.Delivery.Amount.IsKnown() || !row.Delivery.Bonus.IsKnown() || !row.Delivery.StandardFee.IsKnown() || !row.Delivery.Total.IsKnown() {
		return "finance_required_sales_amount_missing"
	}
	if !row.Return.Quantity.IsKnown() || !orders.ExactDecimalEqual(*row.Return.Quantity.Value, "0") {
		return "finance_returns_require_separate_accounting_review"
	}
	for _, value := range []orders.Field[orders.Decimal]{row.Return.Amount, row.Return.Bonus, row.Return.StandardFee, row.Return.BankCoinvestment, row.Return.PickUpPointCoinvestment, row.Return.Stars, row.Return.Total, row.Delivery.BankCoinvestment, row.Delivery.PickUpPointCoinvestment, row.Delivery.Stars} {
		if value.Validate() != nil || !value.IsKnown() || !orders.ExactDecimalEqual(*value.Value, "0") {
			return "finance_nonzero_or_unknown_components_require_review"
		}
	}
	for _, legacy := range []orders.Field[orders.Decimal]{row.Delivery.Compensation, row.Delivery.LegacyCommission, row.Return.Compensation, row.Return.LegacyCommission} {
		if legacy.IsKnown() && !orders.ExactDecimalEqual(*legacy.Value, "0") {
			return "finance_legacy_components_require_review"
		}
	}
	return ""
}

func commissionLineTotal(line orders.CommissionLine, source orders.RealizationAmounts) string {
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
	amount, err := parseDecimal(*source.Amount.Value)
	if err != nil || !gross.equal(amount) {
		return "finance_selected_price_amount_mismatch"
	}
	net, err := parseDecimal(*source.Total.Value)
	if err != nil || !commissionSubtract(gross, reward).equal(net) {
		return "finance_selected_price_total_mismatch"
	}
	return ""
}
