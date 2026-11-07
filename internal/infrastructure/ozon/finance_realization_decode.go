package ozon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

func decodeRealization(data []byte, scope orders.Scope, period orders.RealizationPeriod, now time.Time) (orders.RealizationReport, error) {
	if ordercontract.CheckJSON(data) != nil {
		return orders.RealizationReport{}, orders.ErrSchemaDrift
	}
	root := newOrderFields(data)
	result := root.object("result")
	header := result.object("header")
	report, err := realizationHeader(header, scope, period)
	if err != nil || result.raw["rows"] == nil {
		return orders.RealizationReport{}, orders.ErrSchemaDrift
	}
	rows := result.array("rows")
	if string(result.raw["rows"]) == "null" || len(rows) > maxRealizationRows {
		return orders.RealizationReport{}, orders.ErrSchemaDrift
	}
	report.Rows, err = realizationRows(rows)
	if err != nil || *root.err != nil {
		return orders.RealizationReport{}, orders.ErrSchemaDrift
	}
	report.ContractVersion = realizationContractVersion
	report.UnknownComponents, err = realizationUnknownComponents(root, result, header, rows)
	if err != nil {
		return orders.RealizationReport{}, err
	}
	fingerprint, err := realizationFingerprint(report)
	if err != nil {
		return orders.RealizationReport{}, err
	}
	report.Fingerprint, report.ObservedAt = fingerprint, now.UTC()
	report.Blockers = []orders.Validation{{Field: "commissionreportin", Code: "accounting_mapping_review_required"},
		{Field: "rows", Code: "provider_has_no_declared_totals_or_row_count"},
		{Field: "rows", Code: "v2_report_has_no_fulfillment_scheme_or_posting_identity"}}
	if !realizationPeriodConfirmed(report) {
		report.Blockers = append(report.Blockers, orders.Validation{Field: "period", Code: "report_header_period_not_verified"})
	}
	return report, nil
}

func realizationHeader(header orderFields, scope orders.Scope, period orders.RealizationPeriod) (orders.RealizationReport, error) {
	number, currency := header.text("number"), header.text("currency_sys_name")
	if !number.IsKnown() || !currency.IsKnown() || !realizationText(*number.Value, 256) || (orders.Money{Amount: "0", Currency: *currency.Value}).Validate() != nil {
		return orders.RealizationReport{}, orders.ErrSchemaDrift
	}
	return orders.RealizationReport{Scope: scope, Period: period, Number: *number.Value, Currency: *currency.Value,
		DocumentDate: realizationDate(header, "doc_date"), StartDate: realizationDate(header, "start_date"), StopDate: realizationDate(header, "stop_date")}, nil
}

func realizationRows(fields []orderFields) ([]orders.RealizationRow, error) {
	rows := make([]orders.RealizationRow, 0, len(fields))
	seen := make(map[int64]bool, len(fields))
	for _, field := range fields {
		row, err := realizationRow(field)
		if err != nil || seen[row.Number] {
			return nil, orders.ErrSchemaDrift
		}
		seen[row.Number] = true
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b orders.RealizationRow) int {
		if a.Number < b.Number {
			return -1
		}
		if a.Number > b.Number {
			return 1
		}
		return 0
	})
	return rows, nil
}

func realizationRow(field orderFields) (orders.RealizationRow, error) {
	item := field.object("item")
	number, err := strconv.ParseInt(string(field.raw["rowNumber"]), 10, 32)
	sku, skuErr := strconv.ParseInt(string(item.raw["sku"]), 10, 64)
	if err != nil || number < 0 || skuErr != nil || sku <= 0 {
		return orders.RealizationRow{}, orders.ErrSchemaDrift
	}
	row := orders.RealizationRow{Number: number, SKU: item.id("sku"), OfferID: item.text("offer_id"),
		CommissionRatio: realizationDecimal(field, "commission_ratio"), SellerUnitPrice: realizationDecimal(field, "seller_price_per_instance"),
		Delivery: realizationAmounts(field, "delivery_commission"), Return: realizationAmounts(field, "return_commission")}
	if *field.err != nil || !row.SKU.IsKnown() || !row.OfferID.IsKnown() || !realizationText(*row.OfferID.Value, 256) {
		return orders.RealizationRow{}, orders.ErrSchemaDrift
	}
	return row, nil
}

func realizationFingerprint(report orders.RealizationReport) (string, error) {
	report.Analysis = nil
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
