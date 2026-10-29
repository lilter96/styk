package domain

import "time"

// StockPool identifies a physical resource independently of any marketplace offer.
type StockPool struct {
	Tenant, MoyskladAccount, WarehouseID, ItemKind, ItemID string
}

type StockTarget struct {
	OzonAccount, WarehouseID, ProductID string
}

// MappingReference refers to the confirmed version used at decision time.
type MappingReference struct {
	ID                                       int64
	Version                                  int
	Tenant, OzonAccount, MoyskladAccount     string
	OzonProductID, OfferID, ItemKind, ItemID string
	MoyskladWarehouseID, OzonWarehouseID     string
	Provenance                               string
	ConfirmedAt                              time.Time
}

type StockCalculationRequest struct {
	Pool   StockPool
	Target StockTarget
	Policy StockCalculationPolicy
}

type StockCalculationPolicy struct {
	Version           int
	Unit              string
	SafetyStock       string
	MaxObservationAge time.Duration
	DecisionLifetime  time.Duration
	// Only a scoped controlled-test policy may accept operator assumptions.
	AcceptOperatorAttested bool
	// A scoped, time-limited test policy may explicitly accept a standing
	// operator assumption; this never represents a fresh provider observation.
	TestStandingAttestationMaxAge time.Duration
}

// StockSourceEvidence preserves the exact values and local timestamp of one
// imported warehouse row. ImportID can be present without a matching row.
type StockSourceEvidence struct {
	Tenant, Account, WarehouseID, ItemKind, ItemID string
	ImportID                                       *int64
	ObservationFound                               bool
	Physical, Reserve                              *string
	ObservedAt                                     *time.Time
	// FreshTraversal records a later completed traversal of the same persisted
	// content. It is not a source watermark or a per-row observation time.
	FreshTraversal    *StockTraversalEvidence
	TimeBasis         string
	TraversalComplete bool
}

type StockMarketplaceEvidence struct {
	Tenant, Account, WarehouseID, ProductID string
	ImportID, PostingBatchID                *int64
	StockRowFound                           bool
	Present, Reserved, FreeStock            *int64
	ObservedAt                              *time.Time
	FreshTraversal                          *StockTraversalEvidence
	PostingWindowFrom                       *time.Time
	PostingWindowTo                         *time.Time
}

// StockTraversalEvidence links a scoped completed import operation to its
// deduplicated batch. StartedAt is the conservative freshness boundary; the
// traversal is not necessarily an atomic marketplace snapshot.
type StockTraversalEvidence struct {
	OperationID string
	ImportID    int64
	StartedAt   time.Time
	FinishedAt  time.Time
}

type StockConnectionReference struct {
	Source, Account, Provenance string
	RegisteredAt                time.Time
	IncludeImportHistory        bool
}

type StockProofState string

const (
	ProofUnknown     StockProofState = "unknown"
	ProofVerified    StockProofState = "verified"
	ProofAttested    StockProofState = "operator_attested"
	ProofConflict    StockProofState = "conflict"
	ProofUnsupported StockProofState = "unsupported"
)

// A verified proof is an explicitly supplied assertion with provenance and a
// validity interval. The current import adapter cannot attest the critical
// cross-system proofs; tests can supply synthetic proofs to exercise arithmetic.
type StockProof struct {
	State StockProofState
	// EvidenceClass distinguishes an operator assertion from provider evidence.
	EvidenceClass string
	Reference     string
	AsOf          time.Time
	ValidUntil    time.Time
}

type StockEvidence struct {
	Source            StockSourceEvidence
	Marketplace       StockMarketplaceEvidence
	Connections       []StockConnectionReference
	ProductMapping    *MappingReference
	WarehouseMapping  *MappingReference
	Proofs            map[string]StockProof
	Unreflected       *string
	UnseenBound       *string
	SharedPool        bool
	WarehouseConflict bool
	ProductConflict   bool
	CustomerOrders    *StockCustomerOrderEvidence
}

type StockCustomerOrderEvidence struct {
	BatchID           *int64
	Correlations      []OrderCorrelation
	Truncated         bool
	WindowFrom        *time.Time                   `json:"window_from,omitempty"`
	WindowTo          *time.Time                   `json:"window_to,omitempty"`
	FinishedAt        *time.Time                   `json:"finished_at,omitempty"`
	Positions         []StockObservedOrderPosition `json:"positions,omitempty"`
	Postings          []StockObservedPostingLine   `json:"postings,omitempty"`
	PostingBatchID    *int64                       `json:"posting_batch_id,omitempty"`
	PostingWindowFrom *time.Time                   `json:"posting_window_from,omitempty"`
	PostingWindowTo   *time.Time                   `json:"posting_window_to,omitempty"`
}

// These are bounded observations, not proof that all obligations were seen
// or that an order is disjoint from the source stock reservation.
type StockObservedOrderPosition struct {
	OrderID       string    `json:"order_id"`
	PositionID    string    `json:"position_id"`
	ItemKind      string    `json:"item_kind"`
	ItemID        string    `json:"item_id"`
	WarehouseID   *string   `json:"warehouse_id,omitempty"`
	StateID       *string   `json:"state_id,omitempty"`
	SourceUpdated *string   `json:"source_updated,omitempty"`
	SourceDeleted *string   `json:"source_deleted,omitempty"`
	SourceMoment  *string   `json:"source_moment,omitempty"`
	Applicable    *bool     `json:"applicable,omitempty"`
	Quantity      *string   `json:"quantity"`
	Reserve       *string   `json:"reserve"`
	Shipped       *string   `json:"shipped"`
	ObservedAt    time.Time `json:"observed_at"`
}

type StockObservedPostingLine struct {
	PostingNumber   string     `json:"posting_number"`
	Status          string     `json:"status"`
	WarehouseID     string     `json:"warehouse_id"`
	OfferID         string     `json:"offer_id"`
	SKU             string     `json:"sku"`
	Quantity        int64      `json:"quantity"`
	ObservedAt      time.Time  `json:"observed_at"`
	SourceCreatedAt *time.Time `json:"source_created_at,omitempty"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
}

type StockDecisionStatus string

const (
	StockReady   StockDecisionStatus = "READY_CONDITIONAL"
	StockUnknown StockDecisionStatus = "UNKNOWN"
	StockBlocked StockDecisionStatus = "BLOCKED"
)

type StockCheck struct {
	Code     string
	Passed   bool
	Message  string
	Evidence string
}

type StockDecision struct {
	ID                   int64
	Request              StockCalculationRequest
	Evidence             StockEvidence
	EvaluatedAt          time.Time
	SourceObservationAge *string
	ExpiresAt            *time.Time
	Status               StockDecisionStatus
	Quantity             *string
	ReasonCodes          []string
	Checks               []StockCheck
	ListingMayBeExposed  bool
}
