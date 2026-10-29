package domain

import (
	"errors"
	"strings"
	"time"
)

// StockSyncScope is always a tenant-owned pair of accounts. It cannot be
// inferred from whichever credentials a process happens to have configured.
type StockSyncScope struct {
	Tenant          string `json:"tenant"`
	MoyskladAccount string `json:"moysklad_account"`
	OzonAccount     string `json:"ozon_account"`
}

func (s StockSyncScope) Valid() bool {
	for _, v := range []string{s.Tenant, s.MoyskladAccount, s.OzonAccount} {
		if v == "" || strings.TrimSpace(v) != v || len(v) > 256 {
			return false
		}
	}
	return true
}

type StockSyncProof struct {
	Code       string    `json:"code"`
	Reference  string    `json:"reference"`
	AsOf       time.Time `json:"as_of"`
	ValidUntil time.Time `json:"valid_until"`
}
type StockSyncAttestation struct {
	ProductID   string           `json:"ozon_product_id"`
	WarehouseID string           `json:"ozon_warehouse_id"`
	Unreflected string           `json:"unreflected"`
	UnseenBound string           `json:"unseen_bound"`
	SafetyStock string           `json:"safety_stock"`
	Proofs      []StockSyncProof `json:"proofs"`
	// Nonzero only for a persisted, currently enabled test policy. The manual
	// preview API cannot manufacture or submit this value.
	AutomaticPolicyRevision int64 `json:"automatic_policy_revision,omitempty"`
}
type StockSyncPreviewRequest struct {
	StockSyncScope
	Attestations []StockSyncAttestation `json:"attestations,omitempty"`
}
type StockRecoveryRequest struct {
	StockSyncScope
	OperationID        string    `json:"-"`
	SessionID          string    `json:"-"`
	IdempotencyKey     string    `json:"idempotency_key"`
	ExpectedReadbackAt time.Time `json:"expected_readback_at"`
	ExpectedQuantity   string    `json:"expected_quantity"`
	Reason             string    `json:"reason"`
	Acknowledge        bool      `json:"acknowledge"`
	WorkerStopped      bool      `json:"worker_stopped"`
}
type StockSyncCheck struct {
	Code    string `json:"code"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}
type StockSyncTarget struct {
	ProductID               string           `json:"ozon_product_id"`
	WarehouseID             string           `json:"ozon_warehouse_id"`
	SourceItemID            string           `json:"source_item_id"`
	SourceWarehouseID       string           `json:"source_warehouse_id"`
	ProductMappingVersion   int              `json:"product_mapping_version"`
	WarehouseMappingVersion int              `json:"warehouse_mapping_version"`
	SettlementOperationID   string           `json:"settlement_operation_id,omitempty"`
	SettlementObservedAt    *time.Time       `json:"settlement_observed_at,omitempty"`
	SourceImportID          *int64           `json:"source_import_id,omitempty"`
	Quantity                *string          `json:"quantity,omitempty"`
	SourcePhysical          *string          `json:"source_physical,omitempty"`
	SourceReserve           *string          `json:"source_reserve,omitempty"`
	MarketplaceFree         *int64           `json:"marketplace_free,omitempty"`
	MarketplacePresent      *int64           `json:"marketplace_present,omitempty"`
	MarketplaceReserved     *int64           `json:"marketplace_reserved,omitempty"`
	Status                  string           `json:"status"`
	Checks                  []StockSyncCheck `json:"checks"`
	// Decision is persisted as an immutable snapshot but is removed from public
	// API views. No raw provider response or credential is stored here.
	Decision StockDecision `json:"decision"`
}
type StockSyncReview struct {
	ID string `json:"review_id"`
	StockSyncScope
	Ready                      bool                   `json:"ready"`
	Checks                     []StockSyncCheck       `json:"checks"`
	Targets                    []StockSyncTarget      `json:"targets"`
	CreatedAt                  time.Time              `json:"created_at"`
	ExpiresAt                  time.Time              `json:"expires_at"`
	Attestations               []StockSyncAttestation `json:"attestations,omitempty"`
	MoyskladCredentialRevision int64                  `json:"moysklad_credential_revision"`
	OzonCredentialRevision     int64                  `json:"ozon_credential_revision"`
}
type StockPublicationTarget struct {
	ID                 int64                      `json:"id"`
	OperationID        string                     `json:"operation_id"`
	ProductID          string                     `json:"ozon_product_id"`
	WarehouseID        string                     `json:"ozon_warehouse_id"`
	Quantity           string                     `json:"quantity"`
	State              string                     `json:"state"`
	SafeError          string                     `json:"safe_error,omitempty"`
	DispatchedAt       *time.Time                 `json:"dispatched_at,omitempty"`
	FinishedAt         *time.Time                 `json:"finished_at,omitempty"`
	AcknowledgedAt     *time.Time                 `json:"acknowledged_at,omitempty"`
	ReadbackAt         *time.Time                 `json:"readback_at,omitempty"`
	ReadbackQuantity   *string                    `json:"readback_quantity,omitempty"`
	HTTPStatus         *int                       `json:"http_status,omitempty"`
	ProviderUpdated    *bool                      `json:"provider_updated,omitempty"`
	ProviderErrorCodes []string                   `json:"provider_error_codes,omitempty"`
	Readbacks          []StockPublicationReadback `json:"readbacks,omitempty"`
	Decision           StockDecision              `json:"decision"`
}
type StockPublicationReadback struct {
	ObservedAt time.Time `json:"observed_at"`
	Quantity   *string   `json:"quantity,omitempty"`
	Outcome    string    `json:"outcome"`
}
type StockPublicationRecovery struct {
	ID                  string    `json:"id"`
	OriginalOperationID string    `json:"original_operation_id"`
	NewOperationID      string    `json:"new_operation_id,omitempty"`
	ProductID           string    `json:"ozon_product_id"`
	WarehouseID         string    `json:"ozon_warehouse_id"`
	ObservedQuantity    string    `json:"observed_quantity"`
	ObservedAt          time.Time `json:"observed_at"`
	CreatedAt           time.Time `json:"created_at"`
	PrincipalType       string    `json:"principal_type"`
	Reason              string    `json:"reason"`
}
type StockPublication struct {
	ID string `json:"id"`
	StockSyncScope
	ReviewID         string                   `json:"review_id"`
	State            string                   `json:"state"`
	CreatedAt        time.Time                `json:"created_at"`
	Targets          []StockPublicationTarget `json:"targets"`
	OutboxClaimToken string                   `json:"-"`
}
type StockSyncView struct {
	StockSyncScope
	Enabled         bool                      `json:"enabled"`
	State           string                    `json:"state"`
	Ready           bool                      `json:"ready"`
	Checks          []StockSyncCheck          `json:"checks"`
	Targets         []StockSyncTarget         `json:"targets"`
	LastOperation   *StockPublication         `json:"last_operation,omitempty"`
	LastConfirmedAt *time.Time                `json:"last_confirmed_at,omitempty"`
	Recovery        *StockPublicationRecovery `json:"recovery,omitempty"`
}

var (
	ErrStockSyncInvalid  = errors.New("invalid stock sync proof input")
	ErrStockSyncBlocked  = errors.New("stock sync readiness is not established")
	ErrStockSyncStale    = errors.New("stock sync evidence changed or expired")
	ErrStockSyncConflict = errors.New("stock sync idempotency conflict")
	ErrStockSyncNotFound = errors.New("stock sync scope or operation not found")
)
