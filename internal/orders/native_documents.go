package orders

import (
	"context"
	"encoding/hex"
	"errors"
	"time"
)

var ErrNativeDocumentDeleted = errors.New("native document was deleted; manual review required")

// Native documents record accounting movements, not marketplace delivery
// commands. Their creation must never imply a payment or a courier booking.
type NativeDocumentPolicy struct {
	Enabled             bool      `json:"enabled"`
	AllowPosted         bool      `json:"allow_posted"`
	AllowUnpostedParent bool      `json:"allow_unposted_parent"`
	Actions             []Action  `json:"actions"`
	MaximumQuantity     Decimal   `json:"maximum_quantity"`
	EventMaxAgeSeconds  int64     `json:"event_max_age_seconds"`
	AttestedAt          time.Time `json:"attested_at"`
	ValidUntil          time.Time `json:"valid_until"`
}

type NativeDocumentCorrelation struct {
	Kind       string `json:"kind"`
	SyncID     string `json:"sync_id"`
	Marker     string `json:"marker"`
	DocumentID string `json:"document_id,omitempty"`
}

type NativeMovementEvent struct {
	Kind           string               `json:"kind"`
	OccurredAt     time.Time            `json:"occurred_at"`
	Confirmed      bool                 `json:"confirmed"`
	Digest         string               `json:"digest"`
	ProtectedInput ProtectedActionInput `json:"protected_input"`
}

// The reference is accepted only in an authenticated request and then sealed.
// It is never copied to a native document, preview, receipt or audit payload.
type NativeMovementProposal struct {
	RetryWorkID     string           `json:"retry_work_id,omitempty"`
	PostingNumber   string           `json:"posting_number"`
	Action          Action           `json:"action"`
	Applicable      Field[bool]      `json:"applicable"`
	Lines           []ActionQuantity `json:"lines"`
	DemandWorkID    string           `json:"demand_work_id,omitempty"`
	EventKind       string           `json:"event_kind"`
	EventOccurredAt time.Time        `json:"event_occurred_at"`
	EventConfirmed  bool             `json:"event_confirmed"`
	PlannedMoment   time.Time        `json:"planned_moment"`
	EventReference  string           `json:"event_reference"`
}

type NativeMovementLine struct {
	Identity        LineIdentity      `json:"identity"`
	Mapping         ProductMappingRef `json:"mapping"`
	OrderPositionID string            `json:"order_position_id"`
	BasisPositionID string            `json:"basis_position_id,omitempty"`
	Quantity        Decimal           `json:"quantity"`
	Price           Money             `json:"price"`
	Discount        Field[Decimal]    `json:"discount"`
	VAT             Field[Decimal]    `json:"vat"`
	VATEnabled      Field[bool]       `json:"vat_enabled"`
}

type NativeMovement struct {
	Retry            *NativeRetryAuthorization   `json:"retry,omitempty"`
	ParentEffect     NativeParentEffect          `json:"parent_effect,omitempty"`
	ParentApplicable Field[bool]                 `json:"parent_applicable,omitzero"`
	Consumption      *NativeConsumptionSeal      `json:"consumption,omitempty"`
	Products         []NativeProductRequirements `json:"products"`
	Correlation      NativeDocumentCorrelation   `json:"correlation"`
	SourceBaseline   PositionBaseline            `json:"source_baseline"`
	DemandWorkID     string                      `json:"demand_work_id,omitempty"`
	DemandBasis      *NativeDocumentObservation  `json:"demand_basis,omitempty"`
	OrganizationID   string                      `json:"organization_id"`
	AgentID          string                      `json:"agent_id"`
	StoreID          string                      `json:"store_id"`
	CurrencyID       string                      `json:"currency_id"`
	RateValue        Field[Decimal]              `json:"rate_value"`
	Applicable       Field[bool]                 `json:"applicable"`
	VATEnabled       Field[bool]                 `json:"vat_enabled"`
	VATIncluded      Field[bool]                 `json:"vat_included"`
	Moment           time.Time                   `json:"moment"`
	Lines            []NativeMovementLine        `json:"lines"`
	Event            NativeMovementEvent         `json:"event"`
}

type NativeDocumentPosition struct {
	ID         string         `json:"id"`
	ItemKind   string         `json:"item_kind"`
	ItemID     string         `json:"item_id"`
	Quantity   Decimal        `json:"quantity"`
	Price      Money          `json:"price"`
	Discount   Field[Decimal] `json:"discount"`
	VAT        Field[Decimal] `json:"vat"`
	VATEnabled Field[bool]    `json:"vat_enabled"`
}

type NativeReturnEvidence struct {
	SourceDeleted Field[string]            `json:"source_deleted,omitzero"`
	ID            string                   `json:"id"`
	DemandID      string                   `json:"demand_id"`
	Applicable    Field[bool]              `json:"applicable"`
	Positions     []NativeDocumentPosition `json:"positions"`
	Revision      string                   `json:"revision"`
	ObservedAt    time.Time                `json:"observed_at"`
}

// ReturnsComplete means a bounded complete traversal, never an atomic snapshot.
type NativeDocumentObservation struct {
	SourceDeleted     Field[string]             `json:"source_deleted,omitzero"`
	Scope             Scope                     `json:"scope"`
	Correlation       NativeDocumentCorrelation `json:"correlation"`
	OrganizationID    string                    `json:"organization_id"`
	AgentID           string                    `json:"agent_id"`
	StoreID           string                    `json:"store_id"`
	CurrencyID        string                    `json:"currency_id"`
	RateValue         Field[Decimal]            `json:"rate_value"`
	CustomerOrderID   Field[string]             `json:"customer_order_id"`
	DemandID          Field[string]             `json:"demand_id"`
	Applicable        Field[bool]               `json:"applicable"`
	VATEnabled        Field[bool]               `json:"vat_enabled"`
	VATIncluded       Field[bool]               `json:"vat_included"`
	Moment            Field[time.Time]          `json:"moment"`
	Sum               Field[Money]              `json:"sum"`
	Positions         []NativeDocumentPosition  `json:"positions"`
	Returns           []NativeReturnEvidence    `json:"returns,omitempty"`
	ReturnsComplete   bool                      `json:"returns_complete"`
	ReturnsStartedAt  time.Time                 `json:"returns_started_at"`
	ReturnsFinishedAt time.Time                 `json:"returns_finished_at"`
	Revision          string                    `json:"revision"`
	ObservedAt        time.Time                 `json:"observed_at"`
	Complete          bool                      `json:"complete"`
}

type NativeDocumentBinding struct {
	Parent        TargetObservation         `json:"parent"`
	PostingNumber string                    `json:"posting_number"`
	EventDigest   string                    `json:"event_digest"`
	WorkID        string                    `json:"work_id"`
	Observation   NativeDocumentObservation `json:"observation"`
}

type NativeDocumentRequest struct {
	Scope          Scope
	Correlation    NativeDocumentCorrelation
	IncludeReturns bool
}

type NativeDocumentReader interface {
	ReadNativeDocument(context.Context, NativeDocumentRequest) (NativeDocumentObservation, error)
}

type NativeEventProtector interface {
	ProtectNativeMovementEvent(context.Context, Config, NativeMovementProposal, time.Time) (NativeMovementEvent, error)
}

type NativeEventReader interface {
	VerifyNativeMovementEvent(context.Context, Command) error
}

// NativeInventoryEffect is consumed by the existing physical-pool safety guard.
// A fresh traversal may reuse content; it must start after ReconciledAt.
type NativeInventoryEffect struct {
	PostingNumber string
	Scope         Scope
	WorkID        string
	Kind          string
	State         State
	Applicable    bool
	StoreID       string
	ItemKind      string
	ItemID        string
	DispatchedAt  time.Time
	ReconciledAt  time.Time
}

func (action Action) IsNativeMovement() bool {
	return action == ActionDemandCreate || action == ActionSalesReturnCreate
}

func (action Action) NativeDocumentKind() string {
	if action == ActionDemandCreate {
		return "demand"
	}
	if action == ActionSalesReturnCreate {
		return "salesreturn"
	}
	return ""
}

func (correlation NativeDocumentCorrelation) Validate() error {
	if correlation.Kind != "demand" && correlation.Kind != "salesreturn" {
		return ErrInvalid
	}
	if !validUUID(correlation.SyncID) || !validID(correlation.Marker) || len(correlation.Marker) > 255 {
		return ErrInvalid
	}
	if correlation.DocumentID != "" && !validUUID(correlation.DocumentID) {
		return ErrInvalid
	}
	return nil
}

func (event NativeMovementEvent) Validate(revision int64, posted bool) error {
	digest, err := hex.DecodeString(event.Digest)
	ref, refErr := hex.DecodeString(event.ProtectedInput.Reference)
	if err != nil || len(digest) != 32 || refErr != nil || len(ref) != 32 || event.ProtectedInput.Revision != revision || event.OccurredAt.IsZero() {
		return ErrInvalid
	}
	if posted && (!event.Confirmed || event.Kind != "shipment_dispatched" && event.Kind != "return_received") {
		return ErrBlocked
	}
	if !posted && (event.Kind != "draft_plan" || event.Confirmed) {
		return ErrInvalid
	}
	return nil
}

type NativeDocumentLookup interface {
	OrderNativeDocument(context.Context, Scope, string) (NativeDocumentBinding, error)
}
