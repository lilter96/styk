// Package catalog defines provider-independent catalog facts and reviewed changes.
package catalog

import (
	"context"
	"encoding/json"
	"time"
)

type Provider string

const (
	ProviderMoySklad Provider = "moysklad"
	ProviderOzon     Provider = "ozon"
)

// Scope binds durable work to a tenant and both participating provider accounts.
type Scope struct {
	Tenant          string `json:"tenant"`
	MoyskladAccount string `json:"moysklad_account"`
	OzonAccount     string `json:"ozon_account"`
}

type Account struct {
	Provider Provider `json:"provider"`
	ID       string   `json:"id"`
}

// Identity preserves each provider's identifier namespaces. Names never identify products.
type Identity struct {
	Account    Account `json:"account"`
	EntityType string  `json:"entity_type,omitempty"`
	ID         string  `json:"id"`
	OfferID    string  `json:"offer_id,omitempty"`
	SKU        string  `json:"sku,omitempty"`
}

type Field string

const (
	FieldName        Field = "name"
	FieldDescription Field = "description"
	FieldArticle     Field = "article"
	FieldPrice       Field = "price"
	FieldCurrency    Field = "currency"
	FieldBarcode     Field = "barcode"
	FieldBarcodes    Field = "barcodes"
	FieldCategory    Field = "category"
	FieldAttributes  Field = "attributes"
	FieldUnit        Field = "unit"
	FieldImages      Field = "images"
	FieldDimensions  Field = "dimensions"
	FieldParent      Field = "parent"
	FieldLifecycle   Field = "lifecycle"
)

type FieldState string

const (
	StateAbsent      FieldState = "absent"
	StateKnown       FieldState = "known"
	StateEmpty       FieldState = "empty"
	StateClear       FieldState = "clear"
	StateUnsupported FieldState = "unsupported"
	StateUnknown     FieldState = "unknown"
)

type EvidenceKind string

const (
	EvidenceAPICurated       EvidenceKind = "API_CURATED"
	EvidenceOperatorAttested EvidenceKind = "OPERATOR_ATTESTED"
)

type Provenance struct {
	Kind       EvidenceKind `json:"kind,omitempty"`
	Provider   Provider     `json:"provider,omitempty"`
	AccountID  string       `json:"account_id,omitempty"`
	EntityID   string       `json:"entity_id,omitempty"`
	Revision   string       `json:"revision,omitempty"`
	ObservedAt time.Time    `json:"observed_at,omitempty"`
	Evidence   string       `json:"evidence,omitempty"`
}

// Value distinguishes an observation of emptiness from an explicit request to clear.
// Known Data contains a JSON value, not a provider HTTP payload.
type Value struct {
	State      FieldState      `json:"state"`
	Data       json.RawMessage `json:"data,omitempty"`
	Provenance Provenance      `json:"provenance,omitempty"`
}

type Lifecycle string

const (
	LifecycleActive   Lifecycle = "active"
	LifecycleArchived Lifecycle = "archived"
	LifecycleUnknown  Lifecycle = "unknown"
)

type Product struct {
	Identity   Identity         `json:"identity"`
	Parent     *Identity        `json:"parent,omitempty"`
	Variant    bool             `json:"variant"`
	Lifecycle  Lifecycle        `json:"lifecycle"`
	Fields     map[Field]Value  `json:"fields"`
	Extensions map[string]Value `json:"extensions,omitempty"`
	Revision   string           `json:"revision"`
	ObservedAt time.Time        `json:"observed_at"`
}

type ReadRequest struct {
	Scope      Scope      `json:"scope"`
	Account    Account    `json:"account"`
	Identities []Identity `json:"identities,omitempty"`
	Limit      int        `json:"limit,omitempty"`
	Cursor     string     `json:"cursor,omitempty"`
	Search     string     `json:"search,omitempty"`
}

type Snapshot struct {
	Scope      Scope     `json:"scope"`
	Account    Account   `json:"account"`
	Products   []Product `json:"products"`
	Revision   string    `json:"revision"`
	ObservedAt time.Time `json:"observed_at"`
	Complete   bool      `json:"complete"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type Action string

const (
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionArchive Action = "archive"
)

// Command is the exact persisted, approved intent; adapters cannot add selected fields.
type Command struct {
	ID                     string           `json:"id"`
	Scope                  Scope            `json:"scope"`
	MappingID              string           `json:"mapping_id"`
	MappingVersion         int64            `json:"mapping_version"`
	PolicyRevision         string           `json:"policy_revision"`
	Source                 Identity         `json:"source"`
	Target                 Identity         `json:"target"`
	Action                 Action           `json:"action"`
	Values                 map[Field]Value  `json:"values"`
	Extensions             map[string]Value `json:"extensions,omitempty"`
	Parent                 *Identity        `json:"parent,omitempty"`
	CreateID               string           `json:"create_id,omitempty"`
	CreateExternalCode     string           `json:"create_external_code,omitempty"`
	IdempotencyKey         string           `json:"idempotency_key,omitempty"`
	ExpectedSourceRevision string           `json:"expected_source_revision"`
	ExpectedTargetRevision string           `json:"expected_target_revision"`
}

type ReceiptState string

const (
	ReceiptAccepted ReceiptState = "accepted"
	ReceiptApplied  ReceiptState = "applied"
	ReceiptRejected ReceiptState = "rejected"
	ReceiptPartial  ReceiptState = "partial"
	ReceiptUnknown  ReceiptState = "unknown"
)

type FieldError struct {
	Field   Field  `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ProductReceipt struct {
	ID      string       `json:"id,omitempty"`
	OfferID string       `json:"offer_id,omitempty"`
	Status  string       `json:"status"`
	Errors  []FieldError `json:"errors,omitempty"`
}

type Receipt struct {
	CommandID  string           `json:"command_id"`
	State      ReceiptState     `json:"state"`
	HTTPStatus int              `json:"http_status"`
	TaskID     string           `json:"task_id,omitempty"`
	ExternalID string           `json:"external_id,omitempty"`
	Errors     []FieldError     `json:"errors,omitempty"`
	Products   []ProductReceipt `json:"products,omitempty"`
}

type Reader interface {
	ReadCatalog(context.Context, ReadRequest) (Snapshot, error)
}

type Writer interface {
	WriteCatalog(context.Context, Command) (Receipt, error)
}

// Text preserves an empty observed string as EMPTY. Clear must be requested explicitly.
func Text(value string) Value {
	if value == "" {
		return Empty()
	}
	data, err := json.Marshal(value)
	if err != nil {
		return Value{State: StateUnknown}
	}
	return Value{State: StateKnown, Data: data}
}

func Empty() Value { return Value{State: StateEmpty} }

func Clear() Value { return Value{State: StateClear} }
