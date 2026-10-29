// Package orders defines scoped order facts and durable, reviewed provider intents.
// It does not calculate stock or treat a provider status as an instruction.
package orders

import (
	"errors"
	"time"
)

var (
	ErrInvalid       = errors.New("invalid order synchronization request")
	ErrNotFound      = errors.New("order synchronization resource not found")
	ErrConflict      = errors.New("order synchronization conflict")
	ErrStale         = errors.New("order synchronization evidence changed")
	ErrBlocked       = errors.New("order synchronization is blocked")
	ErrAmbiguousLine = errors.New("ambiguous order line identity")
)

type Scope struct {
	Tenant          string `json:"tenant"`
	OzonAccount     string `json:"ozon_account"`
	MoyskladAccount string `json:"moysklad_account"`
}

type Presence string

const (
	PresenceMissing Presence = "missing"
	PresenceNull    Presence = "null"
	PresenceEmpty   Presence = "empty"
	PresenceValue   Presence = "value"
	PresenceZero    Presence = "zero"
)

// Field preserves provider presence. Missing, null and empty have no Value;
// value and zero have a typed Value. No state requests an implicit clear.
type Field[T any] struct {
	Presence Presence `json:"presence"`
	Value    *T       `json:"value,omitempty"`
}

// Decimal is an exact base-ten number, never a floating-point approximation.
type Decimal string

type Money struct {
	Amount   Decimal `json:"amount"`
	Currency string  `json:"currency"`
}

type Origin string

const (
	OriginOzon     Origin = "ozon_observation"
	OriginMoysklad Origin = "moysklad_observation"
	OriginUser     Origin = "user"
	OriginSTYK     Origin = "styk_mirror"
)

// EvidenceRevision separates local reception time from provider version evidence.
// ProviderUpdatedAt is missing when the endpoint supplies no such timestamp.
type EvidenceRevision struct {
	Revision          string           `json:"revision"`
	ProviderVersion   Field[string]    `json:"provider_version"`
	ProviderUpdatedAt Field[time.Time] `json:"provider_updated_at"`
	ObservedAt        time.Time        `json:"observed_at"`
	Origin            Origin           `json:"origin"`
	SourceCommandID   string           `json:"source_command_id,omitempty"`
	CollectionID      string           `json:"collection_id,omitempty"`
	LocalSequence     int64            `json:"local_sequence,omitempty"`
	CollectionKind    CollectionKind   `json:"collection_kind,omitempty"`
	CheckedAt         time.Time        `json:"checked_at,omitempty"`
}

type CollectionKind string

const (
	CollectionSnapshot     CollectionKind = "snapshot"
	CollectionSerialDetail CollectionKind = "serial_fresh_detail"
)

type Order struct {
	Scope             Scope         `json:"scope"`
	CommercialOrderID string        `json:"commercial_order_id"`
	OrderNumber       Field[string] `json:"order_number"`
	PostingNumbers    []string      `json:"posting_numbers"`
}

// LineIdentity uses provider identifiers, never an array position. ProductID has
// a separate namespace and is supplied by source evidence or a confirmed mapping.
type LineIdentity struct {
	ProviderLineID string `json:"provider_line_id,omitempty"`
	SKU            string `json:"sku,omitempty"`
	OfferID        string `json:"offer_id,omitempty"`
}

type PostingLine struct {
	Identity          LineIdentity   `json:"identity"`
	ProductID         Field[string]  `json:"product_id"`
	Quantity          Decimal        `json:"quantity"`
	CancelledQuantity Field[Decimal] `json:"cancelled_quantity"`
	Price             Field[Money]   `json:"price"`
	Name              Field[string]  `json:"name"`
	VAT               Field[Decimal] `json:"vat"`
}

type PostingTimes struct {
	CreatedAt              Field[time.Time] `json:"created_at"`
	InProcessAt            Field[time.Time] `json:"in_process_at"`
	ShipmentAt             Field[time.Time] `json:"shipment_at"`
	DeliveringAt           Field[time.Time] `json:"delivering_at"`
	DeliveredAt            Field[time.Time] `json:"delivered_at"`
	ShipmentWithoutDelayAt Field[time.Time] `json:"shipment_without_delay_at"`
}

// CapabilityRef names reviewed capability evidence; strings alone never prove it.
type CapabilityRef struct {
	ID                 string        `json:"id"`
	Revision           int64         `json:"revision"`
	WarehouseID        string        `json:"warehouse_id"`
	DeliveryMethodID   string        `json:"delivery_method_id"`
	Scheme             string        `json:"scheme"`
	Carrier            Field[string] `json:"carrier"`
	Destination        Field[string] `json:"destination"`
	Express            Field[bool]   `json:"express"`
	VerifiedDimensions []string      `json:"verified_dimensions"`
	ObservedAt         time.Time     `json:"observed_at"`
	ValidUntil         time.Time     `json:"valid_until"`
}

type Cancellation struct {
	ReasonID       Field[string] `json:"reason_id"`
	Reason         Field[string] `json:"-"`
	ReasonPresence Presence      `json:"reason_presence"`
	Type           Field[string] `json:"type"`
	Initiator      Field[string] `json:"initiator"`
	AfterShipment  Field[bool]   `json:"after_shipment"`
}

// ProtectedCustomer is a reference to separately protected PII, never customer
// names, phones or addresses in normal persistence, logs or console DTOs.
type ProtectedCustomer struct {
	Reference string   `json:"reference"`
	Schema    string   `json:"schema"`
	Presence  Presence `json:"presence"`
}

// Customer and Address are transient, typed PII. Posting and Command exclude
// them from JSON; only the protected secret store may persist their encryption.
type Customer struct {
	Name    Field[string]  `json:"name"`
	Phone   Field[string]  `json:"phone"`
	Email   Field[string]  `json:"email"`
	Address Field[Address] `json:"address"`
}

type Address struct {
	Country    Field[string] `json:"country"`
	Region     Field[string] `json:"region"`
	City       Field[string] `json:"city"`
	PostalCode Field[string] `json:"postal_code"`
	Street     Field[string] `json:"street"`
	House      Field[string] `json:"house"`
	Apartment  Field[string] `json:"apartment"`
}

// PostingSecret contains only authenticated ciphertext. The vault binds Scope,
// purpose, posting number and revision as associated data. Expiry is at most 30d.
type PostingSecret struct {
	Scope         Scope     `json:"scope"`
	PostingNumber string    `json:"posting_number"`
	Revision      int64     `json:"revision"`
	Purpose       string    `json:"purpose"`
	Ciphertext    []byte    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Drift contains only safe schema paths and codes, never unknown field values.
type Drift struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

type Posting struct {
	Scope               Scope               `json:"scope"`
	CommercialOrderID   string              `json:"commercial_order_id"`
	PostingNumber       string              `json:"posting_number"`
	ParentPostingNumber Field[string]       `json:"parent_posting_number"`
	ChildPostingNumbers []string            `json:"child_posting_numbers"`
	OrderNumber         Field[string]       `json:"order_number"`
	Status              Field[string]       `json:"status"`
	Substatus           Field[string]       `json:"substatus"`
	WarehouseID         Field[string]       `json:"warehouse_id"`
	DeliveryMethodID    Field[string]       `json:"delivery_method_id"`
	DeliveryProfile     *CapabilityRef      `json:"delivery_profile,omitempty"`
	AvailableActions    []string            `json:"available_actions"`
	Times               PostingTimes        `json:"times"`
	Cancellation        Field[Cancellation] `json:"cancellation"`
	Lines               []PostingLine       `json:"lines"`
	Customer            ProtectedCustomer   `json:"customer"`
	PII                 *Customer           `json:"-"`
	Evidence            EvidenceRevision    `json:"evidence"`
	Drift               []Drift             `json:"drift"`
}

type ProductMappingRef struct {
	ID            string    `json:"id"`
	Version       int64     `json:"version"`
	Scope         Scope     `json:"scope"`
	OzonProductID string    `json:"ozon_product_id"`
	SKU           string    `json:"sku,omitempty"`
	OfferID       string    `json:"offer_id,omitempty"`
	MoyskladKind  string    `json:"moysklad_kind"`
	MoyskladID    string    `json:"moysklad_id"`
	Confirmed     bool      `json:"confirmed"`
	ConfirmedAt   time.Time `json:"confirmed_at"`
}

type WarehouseMappingRef struct {
	ID              string        `json:"id"`
	Version         int64         `json:"version"`
	Scope           Scope         `json:"scope"`
	OzonWarehouseID string        `json:"ozon_warehouse_id"`
	MoyskladStoreID string        `json:"moysklad_store_id"`
	Confirmed       bool          `json:"confirmed"`
	ConfirmedAt     time.Time     `json:"confirmed_at"`
	Capability      CapabilityRef `json:"capability"`
}

type CredentialRevisions struct {
	Ozon     int64 `json:"ozon"`
	Moysklad int64 `json:"moysklad"`
}

type FieldMapping struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	AttributeID string `json:"attribute_id,omitempty"`
}

// StatusMapping maps a source fact to a configured MoySklad state reference.
// It does not authorize an Ozon action or impose a linear status ordering.
type StatusMapping struct {
	Status    string        `json:"status"`
	Substatus Field[string] `json:"substatus"`
	StateID   string        `json:"state_id"`
}

type Action string

const (
	ActionMirrorCreate   Action = "mirror_create"
	ActionMirrorUpdate   Action = "mirror_update"
	ActionPostingPack    Action = "posting_pack"
	ActionPostingShip    Action = "posting_ship"
	ActionPostingCancel  Action = "posting_cancel"
	ActionPostingDeliver Action = "posting_deliver"
)

// AutomationGrant is scoped to the source account and a stable authenticated
// user ID. Its revision and expiry must be checked again under dispatch locks.
type AutomationGrant struct {
	ID             string     `json:"id"`
	Scope          Scope      `json:"scope"`
	Revision       int64      `json:"revision"`
	ConfigRevision int64      `json:"config_revision"`
	OwnerID        string     `json:"owner_id"`
	SourceAccount  string     `json:"source_account"`
	Actions        []Action   `json:"actions"`
	Fields         []string   `json:"fields"`
	GrantedAt      time.Time  `json:"granted_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

type Config struct {
	Scope                  Scope            `json:"scope"`
	Revision               int64            `json:"revision"`
	Enabled                bool             `json:"enabled"`
	TestEnabled            bool             `json:"test_enabled"`
	IntervalSeconds        int64            `json:"interval_seconds"`
	SourceFreshnessSeconds int64            `json:"source_freshness_seconds"`
	PreviewTTLSeconds      int64            `json:"preview_ttl_seconds"`
	PIIEnabled             bool             `json:"pii_enabled"`
	PIIRetentionSeconds    int64            `json:"pii_retention_seconds"`
	OrganizationID         string           `json:"organization_id"`
	AgentID                string           `json:"agent_id"`
	StoreID                string           `json:"store_id"`
	StateID                string           `json:"state_id"`
	SalesChannelID         string           `json:"sales_channel_id"`
	FieldMappings          []FieldMapping   `json:"field_mappings"`
	StatusMappings         []StatusMapping  `json:"status_mappings"`
	Automation             *AutomationGrant `json:"automation,omitempty"`
	UpdatedAt              time.Time        `json:"updated_at"`
}

type Correlation struct {
	Scope           Scope  `json:"scope"`
	PostingNumber   string `json:"posting_number"`
	SyncID          string `json:"sync_id"`
	Marker          string `json:"marker"`
	CustomerOrderID string `json:"customer_order_id,omitempty"`
}

type AttributeDefinition struct {
	UniqueCode  string `json:"unique_code"`
	AttributeID string `json:"attribute_id"`
	Type        string `json:"type"`
}

type MetadataRegistry struct {
	Scope            Scope                 `json:"scope"`
	Revision         int64                 `json:"revision"`
	Attributes       []AttributeDefinition `json:"attributes"`
	CreationIntentID string                `json:"creation_intent_id,omitempty"`
	ObservedAt       time.Time             `json:"observed_at"`
}

type AttributeWrite struct {
	AttributeID string        `json:"attribute_id"`
	Value       Field[string] `json:"value"`
}

type PositionWrite struct {
	Identity LineIdentity      `json:"identity"`
	Mapping  ProductMappingRef `json:"mapping"`
	Quantity Decimal           `json:"quantity"`
	Reserve  Field[Decimal]    `json:"reserve"`
	Price    Field[Money]      `json:"price"`
	VAT      Field[Decimal]    `json:"vat"`
}

// CustomerOrderWrite deliberately has no sum, paid or shipped fields: those
// values are computed provider facts. No payment or shipment document is implied.
type CustomerOrderWrite struct {
	OrganizationID string           `json:"organization_id"`
	AgentID        string           `json:"agent_id"`
	StoreID        string           `json:"store_id"`
	StateID        string           `json:"state_id"`
	SalesChannelID string           `json:"sales_channel_id"`
	Name           Field[string]    `json:"name"`
	Description    Field[string]    `json:"description"`
	Moment         Field[time.Time] `json:"moment"`
	DeliveryAt     Field[time.Time] `json:"delivery_at"`
	Applicable     Field[bool]      `json:"applicable"`
	VATEnabled     Field[bool]      `json:"vat_enabled"`
	VATIncluded    Field[bool]      `json:"vat_included"`
	Positions      []PositionWrite  `json:"positions"`
	Attributes     []AttributeWrite `json:"attributes"`
}

// PostingAction is an explicit command with reviewed capability evidence.
// Provider status fields are never copied into this command union.
type PostingAction struct {
	Capability CapabilityRef  `json:"capability"`
	Lines      []LineIdentity `json:"lines"`
	ReasonID   Field[string]  `json:"reason_id"`
}

type Command struct {
	ID                      string              `json:"id"`
	Scope                   Scope               `json:"scope"`
	Action                  Action              `json:"action"`
	CommercialOrderID       string              `json:"commercial_order_id"`
	PostingNumber           string              `json:"posting_number"`
	Correlation             Correlation         `json:"correlation"`
	ConfigRevision          int64               `json:"config_revision"`
	Credentials             CredentialRevisions `json:"credentials"`
	AutomationGrantRevision int64               `json:"automation_grant_revision,omitempty"`
	ExpectedSourceRevision  string              `json:"expected_source_revision"`
	ExpectedTargetRevision  string              `json:"expected_target_revision,omitempty"`
	SourceOrigin            Origin              `json:"source_origin"`
	SourceCommandID         string              `json:"source_command_id,omitempty"`
	Warehouse               WarehouseMappingRef `json:"warehouse"`
	CustomerOrder           *CustomerOrderWrite `json:"customer_order,omitempty"`
	PostingAction           *PostingAction      `json:"posting_action,omitempty"`
	PII                     *Customer           `json:"-"`
	IdempotencyKey          string              `json:"idempotency_key"`
	CreatedAt               time.Time           `json:"created_at"`
}

type State string

const (
	StateReady        State = "READY"
	StateQueued       State = "QUEUED"
	StateClaimed      State = "CLAIMED"
	StateDispatching  State = "DISPATCHING"
	StateAcknowledged State = "ACKNOWLEDGED"
	StatePolling      State = "POLLING"
	StateReconciled   State = "RECONCILED"
	StateUnknown      State = "UNKNOWN"
	StateRejected     State = "REJECTED"
	StateBlocked      State = "BLOCKED"
)

type Receipt struct {
	CommandID  string    `json:"command_id"`
	State      State     `json:"state"`
	HTTPStatus int       `json:"http_status,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	ExternalID string    `json:"external_id,omitempty"`
	Code       string    `json:"code,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

type Validation struct {
	Field string `json:"field,omitempty"`
	Code  string `json:"code"`
}

type Decision struct {
	Posting     Posting      `json:"posting"`
	Correlation Correlation  `json:"correlation"`
	Commands    []Command    `json:"commands"`
	Validation  []Validation `json:"validation"`
	Ready       bool         `json:"ready"`
}

type Preview struct {
	ID             string              `json:"id"`
	Scope          Scope               `json:"scope"`
	ConfigRevision int64               `json:"config_revision"`
	Credentials    CredentialRevisions `json:"credentials"`
	Decisions      []Decision          `json:"decisions"`
	Actor          string              `json:"-"`
	Automatic      bool                `json:"automatic"`
	CreatedAt      time.Time           `json:"created_at"`
	ExpiresAt      time.Time           `json:"expires_at"`
}

type Operation struct {
	ID        string    `json:"id"`
	Scope     Scope     `json:"scope"`
	PreviewID string    `json:"preview_id"`
	State     State     `json:"state"`
	Targets   []Work    `json:"targets"`
	CreatedAt time.Time `json:"created_at"`
}

type Work struct {
	ID                 string              `json:"id"`
	OperationID        string              `json:"operation_id"`
	Scope              Scope               `json:"scope"`
	PreviewID          string              `json:"preview_id"`
	ConfigRevision     int64               `json:"config_revision"`
	Credentials        CredentialRevisions `json:"credentials"`
	Command            Command             `json:"command"`
	CommandFingerprint string              `json:"command_fingerprint"`
	State              State               `json:"state"`
	Receipt            Receipt             `json:"receipt"`
	Automatic          bool                `json:"automatic"`
	ClaimToken         string              `json:"-"`
	LeaseUntil         time.Time           `json:"lease_until"`
	DispatchCount      int                 `json:"dispatch_count"`
	NextRunAt          time.Time           `json:"next_run_at"`
	Code               string              `json:"code,omitempty"`
}
