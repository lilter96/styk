package orders

import (
	"context"
	"time"
)

type CommissionPriceBasis string

const (
	CommissionSellerPrice         CommissionPriceBasis = "SELLER_PRICE"
	CommissionRealizedPrice       CommissionPriceBasis = "REALIZED_PRICE"
	CommissionZeroOtherComponents                      = "ZERO_ONLY"
	CommissionPartialReview                            = "PARTIAL_REVIEW"
	CommissionReturnsRequireZero                       = "REQUIRE_ZERO"
	CommissionReturnsNative                            = "COMMISSION_RETURN"
	MaxCommissionLines                                 = 1000
)

// CommissionPolicy authorizes a draft monthly report independently of mirror
// and physical movement permissions. Tax and price basis are accountant choices.
type CommissionPolicy struct {
	ID                    string               `json:"id"`
	Scope                 Scope                `json:"scope"`
	Revision              int64                `json:"revision"`
	ConfigRevision        int64                `json:"config_revision"`
	OwnerID               string               `json:"owner_id"`
	Enabled               bool                 `json:"enabled"`
	Automatic             bool                 `json:"automatic"`
	GrantedAt             time.Time            `json:"granted_at"`
	OrganizationID        string               `json:"organization_id"`
	AgentID               string               `json:"agent_id"`
	ContractID            string               `json:"contract_id"`
	CurrencyID            string               `json:"currency_id"`
	CurrencyISO           string               `json:"currency_iso"`
	OrganizationAccountID string               `json:"organization_account_id,omitempty"`
	AgentAccountID        string               `json:"agent_account_id,omitempty"`
	RateValue             Decimal              `json:"rate_value"`
	PriceBasis            CommissionPriceBasis `json:"price_basis"`
	VATEnabled            Field[bool]          `json:"vat_enabled"`
	VATIncluded           Field[bool]          `json:"vat_included"`
	VAT                   Decimal              `json:"vat"`
	OtherComponents       string               `json:"other_components"`
	ReturnTreatment       string               `json:"return_treatment,omitempty"`
}

type CommissionContract struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	OrganizationID        string `json:"organization_id"`
	AgentID               string `json:"agent_id"`
	CurrencyID            string `json:"currency_id"`
	ContractType          string `json:"contract_type"`
	RewardType            string `json:"reward_type"`
	Archived              bool   `json:"archived"`
	OrganizationAccountID string `json:"organization_account_id,omitempty"`
	AgentAccountID        string `json:"agent_account_id,omitempty"`
}

type CommissionMetadata struct {
	Scope      Scope                `json:"scope"`
	Contracts  []CommissionContract `json:"contracts"`
	Truncated  bool                 `json:"truncated"`
	ObservedAt time.Time            `json:"observed_at"`
}

// References retain only identity/version evidence, never bank details or raw
// party records. The provider verifies all references in the same MS account.
type CommissionReferences struct {
	Scope                  Scope     `json:"scope"`
	OrganizationID         string    `json:"organization_id"`
	AgentID                string    `json:"agent_id"`
	ContractID             string    `json:"contract_id"`
	CurrencyID             string    `json:"currency_id"`
	CurrencyISO            string    `json:"currency_iso"`
	OrganizationAccountID  string    `json:"organization_account_id,omitempty"`
	AgentAccountID         string    `json:"agent_account_id,omitempty"`
	ContractType           string    `json:"contract_type"`
	ContractOrganizationID string    `json:"contract_organization_id"`
	ContractAgentID        string    `json:"contract_agent_id"`
	ContractCurrencyID     string    `json:"contract_currency_id"`
	ContractRewardType     string    `json:"contract_reward_type"`
	Revision               string    `json:"revision"`
	ObservedAt             time.Time `json:"observed_at"`
	Complete               bool      `json:"complete"`
}

type CommissionLine struct {
	SourceRow int64             `json:"source_row"`
	Mapping   ProductMappingRef `json:"mapping"`
	Quantity  Decimal           `json:"quantity"`
	Price     Money             `json:"price"`
	Reward    Money             `json:"reward"`
}

type CommissionTotals struct {
	Currency string  `json:"currency"`
	Gross    Decimal `json:"gross"`
	Reward   Decimal `json:"reward"`
	Net      Decimal `json:"net"`
}

type CommissionPlan struct {
	Returns        []CommissionLine     `json:"returns,omitempty"`
	ReturnTotals   *CommissionTotals    `json:"return_totals,omitempty"`
	Accounting     *FinancialLedger     `json:"accounting,omitempty"`
	ID             string               `json:"id"`
	Scope          Scope                `json:"scope"`
	ConfigRevision int64                `json:"config_revision"`
	PolicyRevision int64                `json:"policy_revision"`
	Credentials    CredentialRevisions  `json:"credentials"`
	Policy         CommissionPolicy     `json:"policy"`
	Source         RealizationReport    `json:"source"`
	References     CommissionReferences `json:"references"`
	ExternalCode   string               `json:"external_code"`
	Lines          []CommissionLine     `json:"lines"`
	Totals         CommissionTotals     `json:"totals"`
	Fingerprint    string               `json:"fingerprint"`
	CreatedAt      time.Time            `json:"created_at"`
	ExpiresAt      time.Time            `json:"expires_at"`
}

type CommissionObservedLine struct {
	ID         string  `json:"id"`
	ItemKind   string  `json:"item_kind"`
	ItemID     string  `json:"item_id"`
	Quantity   Decimal `json:"quantity"`
	Price      Money   `json:"price"`
	Reward     Money   `json:"reward"`
	VAT        Decimal `json:"vat"`
	VATEnabled bool    `json:"vat_enabled"`
}

type CommissionObservation struct {
	Returns      []CommissionObservedLine `json:"returns,omitempty"`
	Scope        Scope                    `json:"scope"`
	DocumentID   string                   `json:"document_id"`
	ExternalCode string                   `json:"external_code"`
	References   CommissionReferences     `json:"references"`
	Applicable   bool                     `json:"applicable"`
	VATEnabled   bool                     `json:"vat_enabled"`
	VATIncluded  bool                     `json:"vat_included"`
	RewardType   string                   `json:"reward_type"`
	RateValue    Decimal                  `json:"rate_value"`
	Moment       string                   `json:"moment"`
	PeriodStart  string                   `json:"period_start"`
	PeriodEnd    string                   `json:"period_end"`
	Lines        []CommissionObservedLine `json:"lines"`
	Gross        Money                    `json:"gross"`
	Net          Money                    `json:"net"`
	ReturnCount  int                      `json:"return_count"`
	Revision     string                   `json:"revision"`
	ObservedAt   time.Time                `json:"observed_at"`
	Complete     bool                     `json:"complete"`
}

type CommissionReader interface {
	ReadCommissionMetadata(context.Context, Scope) (CommissionMetadata, error)
	ReadCommissionReferences(context.Context, Scope, CommissionPolicy) (CommissionReferences, error)
	ReadCommissionDraft(context.Context, CommissionPlan, string) (CommissionObservation, error)
}

type CommissionWriter interface {
	WriteCommissionDraft(context.Context, CommissionPlan, func(context.Context) (bool, error)) (Receipt, error)
}
