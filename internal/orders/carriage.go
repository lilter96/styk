package orders

import (
	"context"
	"time"
)

type CarriageAction string

const (
	CarriageCreate  CarriageAction = "CREATE"
	CarriageApprove CarriageAction = "APPROVE"
)

type CarriageInput struct {
	Action                   CarriageAction `json:"action"`
	DeliveryMethodID         string         `json:"delivery_method_id"`
	DepartureDate            string         `json:"departure_date,omitempty"`
	CarriageID               string         `json:"carriage_id,omitempty"`
	AllBLRTraceable          *bool          `json:"all_blr_traceable,omitempty"`
	ContainersCount          *int32         `json:"containers_count,omitempty"`
	TrustAcceptanceConfirmed bool           `json:"trust_acceptance_confirmed,omitempty"`
}

type CarriageHeader struct {
	CarriageID       string    `json:"carriage_id"`
	CompanyID        string    `json:"company_id"`
	DeliveryMethodID string    `json:"delivery_method_id"`
	WarehouseID      string    `json:"warehouse_id"`
	Status           string    `json:"status"`
	AvailableActions []string  `json:"available_actions"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	DepartureDate    string    `json:"departure_date"`
	ContainersCount  *int32    `json:"containers_count,omitempty"`
	FirstMileType    string    `json:"first_mile_type"`
	IntegrationType  string    `json:"integration_type"`
}

type CarriageMember struct {
	PostingNumber     string         `json:"posting_number"`
	Scheme            string         `json:"scheme"`
	WarehouseID       string         `json:"warehouse_id"`
	DeliveryMethodID  string         `json:"delivery_method_id"`
	Capability        *CapabilityRef `json:"capability,omitempty"`
	Status            string         `json:"status"`
	Ready             bool           `json:"ready"`
	Lines             []PostingLine  `json:"lines"`
	SourceFingerprint string         `json:"source_fingerprint"`
	Blockers          []Validation   `json:"blockers"`
}

// CarriageSnapshot is observational evidence. Neither document formation nor
// an empty provider error establishes physical handoff or stock movement.
type CarriageSnapshot struct {
	Scope                  Scope            `json:"scope"`
	DeliveryMethodID       string           `json:"delivery_method_id"`
	WarehouseID            string           `json:"warehouse_id"`
	CarriageID             string           `json:"carriage_id,omitempty"`
	Header                 *CarriageHeader  `json:"header,omitempty"`
	Members                []CarriageMember `json:"members"`
	CandidateIDs           []string         `json:"candidate_ids"`
	ContractVersion        string           `json:"contract_version"`
	ObservedMethodCount    int              `json:"observed_method_count"`
	SourceShapeFingerprint string           `json:"source_shape_fingerprint,omitempty"`
	Complete               bool             `json:"complete"`
	ObservedAt             time.Time        `json:"observed_at"`
	Fingerprint            string           `json:"fingerprint"`
	Blockers               []Validation     `json:"blockers"`
}

type CarriagePlan struct {
	ID    string `json:"id"`
	Scope Scope  `json:"scope"`
	CarriageInput
	WarehouseID           string              `json:"warehouse_id"`
	Members               []CarriageMember    `json:"members"`
	Snapshot              CarriageSnapshot    `json:"snapshot"`
	MembershipFingerprint string              `json:"membership_fingerprint"`
	Fingerprint           string              `json:"fingerprint"`
	ConfigRevision        int64               `json:"config_revision"`
	Credentials           CredentialRevisions `json:"credentials"`
	CreatedAt             time.Time           `json:"created_at"`
	ExpiresAt             time.Time           `json:"expires_at"`
}

type CarriagePreview struct {
	ID           string        `json:"id"`
	Plan         CarriagePlan  `json:"plan"`
	Validation   []Validation  `json:"validation"`
	ExistingWork *CarriageWork `json:"existing_work,omitempty"`
	Actor        string        `json:"-"`
}

type CarriageReader interface {
	ReadCarriageBatch(context.Context, Scope, CarriageInput) (CarriageSnapshot, error)
	ReadCarriage(context.Context, Scope, string) (CarriageSnapshot, error)
}

type CarriageWriter interface {
	WriteCarriage(context.Context, CarriagePlan, func(context.Context) (bool, error)) (Receipt, error)
}
