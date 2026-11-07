package ordermoney

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// ClassifyRealization preserves every source component independently of owner
// policy or mapping availability. A classified residual is not a native fact.
func ClassifyRealization(source orders.RealizationReport) (orders.FinancialLedger, error) {
	ledger := orders.FinancialLedger{SourceFamily: "REALIZATION", Version: "realization-ledger-v1", SourceFingerprint: source.Fingerprint, Period: source.Period, Currency: source.Currency, RowsTotal: len(source.Rows), Rows: make([]orders.FinancialLedgerRow, 0, len(source.Rows)), Components: make([]orders.FinancialComponent, 0, len(source.Rows)*24), Reconciliation: make([]orders.FinancialReconciliation, 0, len(source.Rows)*2)}
	fingerprint, err := CommissionSourceFingerprint(source)
	if err != nil || len(source.Fingerprint) != 64 || fingerprint != source.Fingerprint {
		return ledger, orders.ErrInvalid
	}
	seen := make(map[int64]bool, len(source.Rows))
	for _, row := range source.Rows {
		if seen[row.Number] {
			return ledger, orders.ErrInvalid
		}
		seen[row.Number] = true
		if err := appendFinancialRow(&ledger, source, row); err != nil {
			return ledger, err
		}
	}
	for _, unknown := range source.UnknownComponents {
		component := orders.FinancialComponent{ID: financialComponentID(source.Fingerprint, unknown.SourceRow, unknown.Path), SourceRow: unknown.SourceRow, Side: financialUnknownSide(unknown.Path), Component: unknown.Path, Category: "UNKNOWN", Role: "UNKNOWN", Value: unknown.Value, Currency: source.Currency, ValueDigest: unknown.ValueDigest}
		if err := classifyFinancialComponent(&component); err != nil {
			return ledger, err
		}
		component.Disposition, component.Blocker = "UNRECOGNIZED", "finance_unknown_component_requires_review"
		ledger.Components = append(ledger.Components, component)
		ledger.UnknownComponents++
	}
	ledger, err = finishFinancialLedger(ledger)
	ledger.Complete = false
	return ledger, err
}

func appendFinancialRow(ledger *orders.FinancialLedger, source orders.RealizationReport, row orders.RealizationRow) error {
	ledger.Rows = append(ledger.Rows, orders.FinancialLedgerRow{SourceRow: row.Number, Classification: financialRowClassification(row), NativeStatus: "UNMAPPED", Blockers: []string{}})
	for _, component := range financialRowComponents(row) {
		component.ID, component.SourceRow, component.Currency = financialComponentID(source.Fingerprint, row.Number, component.Side+"."+component.Component), row.Number, source.Currency
		if err := classifyFinancialComponent(&component); err != nil {
			return err
		}
		ledger.Components = append(ledger.Components, component)
	}
	for _, side := range []struct {
		name    string
		amounts orders.RealizationAmounts
	}{{"SALE", row.Delivery}, {"RETURN", row.Return}} {
		reconciled, err := financialReconciliation(row.Number, side.name, side.amounts)
		if err != nil {
			return err
		}
		if len(source.UnknownComponents) > 0 {
			reconciled.ComponentStatus = "UNKNOWN"
		}
		ledger.Reconciliation = append(ledger.Reconciliation, reconciled)
	}
	ledger.RowsClassified++
	return nil
}

func financialComponentID(fingerprint string, row int64, path string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", fingerprint, row, path)))
	return hex.EncodeToString(hash[:])
}

func classifyFinancialComponent(component *orders.FinancialComponent) error {
	if component.Value.Validate() != nil {
		return orders.ErrInvalid
	}
	component.Disposition, component.Sign = "UNREPRESENTED", "UNKNOWN"
	component.TaxTreatment = "SOURCE_UNSPECIFIED"
	if component.Role == "QUANTITY" || component.Role == "PERCENT" {
		component.Currency = ""
		component.TaxTreatment = "NOT_MONETARY"
	}
	if !component.Value.IsKnown() {
		if component.Role == "QUANTITY" || component.Role == "UNIT_PRICE" || component.Role == "PERCENT" {
			component.Disposition = "REFERENCE"
			return nil
		}
		component.Blocker = "finance_component_value_unknown"
		return nil
	}
	value, err := parseDecimal(*component.Value.Value)
	if err != nil {
		return err
	}
	switch value.coefficient.Sign() {
	case -1:
		component.Sign = "NEGATIVE"
	case 0:
		component.Sign, component.Disposition = "ZERO", "ZERO"
	case 1:
		component.Sign = "POSITIVE"
	}
	if component.Role == "QUANTITY" || component.Role == "UNIT_PRICE" || component.Role == "PERCENT" {
		component.Disposition = "REFERENCE"
	}
	return nil
}

func financialUnknownSide(path string) string {
	if strings.HasPrefix(path, "delivery_commission.") {
		return "SALE"
	}
	if strings.HasPrefix(path, "return_commission.") {
		return "RETURN"
	}
	if strings.HasPrefix(path, "row.") || strings.HasPrefix(path, "item.") {
		return "ROW"
	}
	return "HEADER"
}

func financialRowClassification(row orders.RealizationRow) string {
	sale, returned := row.Delivery.Quantity, row.Return.Quantity
	for _, field := range []orders.Field[orders.Decimal]{sale, returned, row.Delivery.Amount, row.Return.Amount} {
		if field.IsKnown() {
			value, err := parseDecimal(*field.Value)
			if err != nil {
				return "UNKNOWN"
			}
			if value.coefficient.Sign() < 0 {
				return "CORRECTION"
			}
		}
	}
	s, r := sale.IsKnown() && !orders.ExactDecimalEqual(*sale.Value, "0"), returned.IsKnown() && !orders.ExactDecimalEqual(*returned.Value, "0")
	if s && r {
		return "MIXED"
	}
	if s {
		return "SALE"
	}
	if r {
		return "RETURN"
	}
	if !sale.IsKnown() || !returned.IsKnown() {
		return "UNKNOWN"
	}
	for _, value := range []orders.Field[orders.Decimal]{row.Delivery.Amount, row.Delivery.StandardFee, row.Delivery.Bonus, row.Delivery.Total, row.Return.Amount, row.Return.StandardFee, row.Return.Bonus, row.Return.Total} {
		if value.IsKnown() && !orders.ExactDecimalEqual(*value.Value, "0") {
			return "ADJUSTMENT"
		}
	}
	return "ZERO"
}

func finishFinancialLedger(ledger orders.FinancialLedger) (orders.FinancialLedger, error) {
	ledger.ComponentsTotal, ledger.ComponentsRepresented, ledger.ComponentsUnrepresented = len(ledger.Components), 0, 0
	for _, component := range ledger.Components {
		if component.Disposition == "UNREPRESENTED" || component.Disposition == "UNRECOGNIZED" {
			ledger.ComponentsUnrepresented++
		} else {
			ledger.ComponentsRepresented++
		}
	}
	ledger.RowsMappedToNative, ledger.RowsPartial, ledger.RowsUnmapped = 0, 0, 0
	for _, row := range ledger.Rows {
		switch row.NativeStatus {
		case "MAPPED", "ZERO":
			ledger.RowsMappedToNative++
		case "PARTIAL":
			ledger.RowsPartial++
		default:
			ledger.RowsUnmapped++
		}
	}
	ledger.Complete = ledger.ComponentsUnrepresented == 0 && ledger.RowsUnmapped == 0 && ledger.RowsPartial == 0
	var err error
	ledger.CategoryTotals, err = financialCategoryTotals(financialTotalComponents(ledger))
	slices.SortFunc(ledger.Components, func(a, b orders.FinancialComponent) int {
		if a.SourceRow < b.SourceRow {
			return -1
		}
		if a.SourceRow > b.SourceRow {
			return 1
		}
		return compareComponentPath(a, b)
	})
	if err != nil {
		return ledger, err
	}
	return ledger, ledger.Validate()
}

func financialTotalComponents(ledger orders.FinancialLedger) []orders.FinancialComponent {
	components := slices.Clone(ledger.Components)
	for _, item := range ledger.Reconciliation {
		components = append(components, orders.FinancialComponent{Category: "COMMISSION_REWARD", Side: item.Side, Role: "DERIVED", Currency: ledger.Currency, Value: item.Reward})
	}
	return components
}

func compareComponentPath(a, b orders.FinancialComponent) int {
	if a.Side+a.Component < b.Side+b.Component {
		return -1
	}
	if a.Side+a.Component > b.Side+b.Component {
		return 1
	}
	return 0
}
