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
	ProofConflict    StockProofState = "conflict"
	ProofUnsupported StockProofState = "unsupported"
)

// A verified proof is an explicitly supplied assertion with provenance and a
// validity interval. The current import adapter cannot attest the critical
// cross-system proofs; tests can supply synthetic proofs to exercise arithmetic.
type StockProof struct {
	State      StockProofState
	Reference  string
	AsOf       time.Time
	ValidUntil time.Time
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
	BatchID      *int64
	Correlations []OrderCorrelation
	Truncated    bool
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
