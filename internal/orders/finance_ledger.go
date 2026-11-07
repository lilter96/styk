package orders

// FinancialLedger classifies realization components without equating overlapping
// prices, totals, fees and quantities to a single fictitious monetary balance.
type FinancialLedger struct {
	SourceFamily            string                    `json:"source_family"`
	Version                 string                    `json:"version"`
	SourceFingerprint       string                    `json:"source_fingerprint"`
	Period                  RealizationPeriod         `json:"period"`
	Currency                string                    `json:"currency"`
	RowsTotal               int                       `json:"rows_total"`
	RowsClassified          int                       `json:"rows_classified"`
	RowsMappedToNative      int                       `json:"rows_mapped_to_native"`
	RowsPartial             int                       `json:"rows_partial"`
	RowsUnmapped            int                       `json:"rows_unmapped"`
	ComponentsTotal         int                       `json:"components_total"`
	ComponentsRepresented   int                       `json:"components_represented"`
	ComponentsUnrepresented int                       `json:"components_unrepresented"`
	UnknownComponents       int                       `json:"unknown_components"`
	Complete                bool                      `json:"complete"`
	Rows                    []FinancialLedgerRow      `json:"rows"`
	Components              []FinancialComponent      `json:"components"`
	CategoryTotals          []FinancialCategoryTotal  `json:"category_totals"`
	Reconciliation          []FinancialReconciliation `json:"reconciliation"`
}

type FinancialComponent struct {
	ID             string         `json:"id"`
	SourceRow      int64          `json:"source_row"`
	Side           string         `json:"side"`
	Component      string         `json:"component"`
	Category       string         `json:"category"`
	Role           string         `json:"role"`
	Value          Field[Decimal] `json:"value"`
	Currency       string         `json:"currency"`
	Sign           string         `json:"sign"`
	Disposition    string         `json:"disposition"`
	Blocker        string         `json:"blocker,omitempty"`
	ValueDigest    string         `json:"value_digest,omitempty"`
	SourcePresence Presence       `json:"source_presence,omitempty"`
	TaxTreatment   string         `json:"tax_treatment"`
}

type FinancialLedgerRow struct {
	SourceRow      int64    `json:"source_row"`
	Classification string   `json:"classification"`
	NativeStatus   string   `json:"native_status"`
	Blockers       []string `json:"blockers"`
}

type FinancialCategoryTotal struct {
	Category string  `json:"category"`
	Side     string  `json:"side"`
	Role     string  `json:"role"`
	Currency string  `json:"currency"`
	Sum      Decimal `json:"sum"`
	Known    int     `json:"known"`
	Missing  int     `json:"missing"`
	Null     int     `json:"null"`
	Positive int     `json:"positive"`
	Negative int     `json:"negative"`
	Zero     int     `json:"zero"`
}

type FinancialReconciliation struct {
	ComponentFormula     string         `json:"component_formula,omitempty"`
	ComponentExpectedNet Field[Decimal] `json:"component_expected_net"`
	ComponentDifference  Field[Decimal] `json:"component_difference"`
	ComponentStatus      string         `json:"component_status"`
	SourceRow            int64          `json:"source_row"`
	Side                 string         `json:"side"`
	Gross                Field[Decimal] `json:"gross"`
	Reward               Field[Decimal] `json:"reward"`
	ExpectedNet          Field[Decimal] `json:"expected_net"`
	SourceNet            Field[Decimal] `json:"source_net"`
	Difference           Field[Decimal] `json:"difference"`
	Status               string         `json:"status"`
}

// Unknown fields retain their exact numeric value where applicable and a
// canonical value digest otherwise. Arbitrary new text is not persisted as PII.
type RealizationUnknownComponent struct {
	SourceRow   int64          `json:"source_row"`
	Path        string         `json:"path"`
	Kind        string         `json:"kind"`
	Value       Field[Decimal] `json:"value"`
	ValueDigest string         `json:"value_digest"`
}

func (ledger FinancialLedger) Validate() error {
	if ledger.SourceFamily != "REALIZATION" || ledger.Version != "realization-ledger-v1" || len(ledger.SourceFingerprint) != 64 || ledger.RowsTotal != len(ledger.Rows) || ledger.RowsClassified != ledger.RowsTotal || ledger.RowsMappedToNative+ledger.RowsPartial+ledger.RowsUnmapped != ledger.RowsTotal || ledger.ComponentsTotal != len(ledger.Components) || ledger.ComponentsRepresented+ledger.ComponentsUnrepresented != ledger.ComponentsTotal {
		return ErrInvalid
	}
	if ledger.Complete && (ledger.ComponentsUnrepresented != 0 || ledger.UnknownComponents != 0 || ledger.RowsPartial != 0 || ledger.RowsUnmapped != 0) {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(ledger.Components))
	unknown, unrepresented := 0, 0
	for _, component := range ledger.Components {
		if len(component.ID) != 64 || seen[component.ID] || component.Value.Validate() != nil || !component.currencyMatches(ledger.Currency) {
			return ErrInvalid
		}
		seen[component.ID] = true
		if component.Category == "UNKNOWN" {
			if len(component.ValueDigest) != 64 {
				return ErrInvalid
			}
			unknown++
		}
		switch component.Disposition {
		case "UNREPRESENTED", "UNRECOGNIZED":
			unrepresented++
		case "REFERENCE", "ZERO", "NOT_APPLICABLE", "NATIVE_SALE", "NATIVE_RETURN", "NATIVE_REWARD":
		default:
			return ErrInvalid
		}
	}
	if unknown != ledger.UnknownComponents || unrepresented != ledger.ComponentsUnrepresented {
		return ErrInvalid
	}
	return nil
}

func (component FinancialComponent) currencyMatches(currency string) bool {
	return component.Currency == currency || component.Currency == "" && (component.Role == "QUANTITY" || component.Role == "PERCENT")
}
