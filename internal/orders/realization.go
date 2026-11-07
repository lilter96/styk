package orders

import (
	"context"
	"time"
)

// RealizationPeriod identifies the authoritative monthly report, independently
// of posting delivery states and accrual analytics.
type RealizationPeriod struct {
	Year  int `json:"year"`
	Month int `json:"month"`
}

func (p RealizationPeriod) Validate(now time.Time) error {
	if p.Year < 2023 || p.Year > 9999 || p.Month < 1 || p.Month > 12 || p.Year == 2023 && p.Month < 8 || now.IsZero() {
		return ErrInvalid
	}
	start := time.Date(p.Year, time.Month(p.Month), 1, 0, 0, 0, 0, time.UTC)
	current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !start.Before(current) {
		return ErrBlocked // The current accounting period is not yet closed.
	}
	return nil
}

type RealizationRequest struct {
	Scope  Scope             `json:"scope"`
	Period RealizationPeriod `json:"period"`
}

// Optional amounts retain missing/null/zero distinctions. JSON numbers are
// decoded as exact decimal text; no binary floating-point conversion occurs.
type RealizationAmounts struct {
	Presence         Presence       `json:"presence"`
	Quantity         Field[Decimal] `json:"quantity"`
	Amount           Field[Decimal] `json:"amount"`
	Bonus            Field[Decimal] `json:"bonus"`
	LegacyCommission Field[Decimal] `json:"legacy_commission"`
	Compensation     Field[Decimal] `json:"compensation"`
	PricePerInstance Field[Decimal] `json:"price_per_instance"`
	StandardFee      Field[Decimal] `json:"standard_fee"`
	BankCoinvestment Field[Decimal] `json:"bank_coinvestment"`
	// This live-observed extension is absent from the published base schema.
	// Omit only genuine absence to preserve old immutable source/plan hashes.
	PickUpPointCoinvestment Field[Decimal] `json:"pick_up_point_coinvestment,omitzero"`
	Stars                   Field[Decimal] `json:"stars"`
	Total                   Field[Decimal] `json:"total"`
}

type RealizationRow struct {
	Number          int64              `json:"number"`
	SKU             Field[string]      `json:"sku"`
	OfferID         Field[string]      `json:"offer_id"`
	CommissionRatio Field[Decimal]     `json:"commission_ratio"`
	SellerUnitPrice Field[Decimal]     `json:"seller_unit_price"`
	Delivery        RealizationAmounts `json:"delivery"`
	Return          RealizationAmounts `json:"return"`
}

// RealizationReport is a sanitized review of provider facts. It is not an
// applied MoySklad commissionreportin, a payment, or physical return evidence.
type RealizationReport struct {
	Evidence          *FinancialEvidenceRecord      `json:"-"`
	UnknownComponents []RealizationUnknownComponent `json:"unknown_components,omitempty"`
	Analysis          *FinancialLedger              `json:"analysis,omitempty"`
	Scope             Scope                         `json:"scope"`
	Period            RealizationPeriod             `json:"period"`
	Number            string                        `json:"number"`
	Currency          string                        `json:"currency"`
	DocumentDate      Field[string]                 `json:"document_date"`
	StartDate         Field[string]                 `json:"start_date"`
	StopDate          Field[string]                 `json:"stop_date"`
	Rows              []RealizationRow              `json:"rows"`
	Fingerprint       string                        `json:"fingerprint"`
	ContractVersion   string                        `json:"contract_version"`
	ObservedAt        time.Time                     `json:"observed_at"`
	Blockers          []Validation                  `json:"blockers"`
}

type RealizationReader interface {
	ReadRealizationReport(context.Context, Scope, RealizationPeriod) (RealizationReport, error)
}

type RealizationReadError struct {
	Code  string
	Cause error
}

func (e *RealizationReadError) Error() string { return "authoritative monthly report unavailable" }
func (e *RealizationReadError) Unwrap() error { return e.Cause }
