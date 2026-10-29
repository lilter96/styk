package orders

import (
	"context"
	"time"
)

type SaveConfigRequest struct {
	Config           Config `json:"config"`
	ExpectedRevision int64  `json:"expected_revision"`
	Actor            string `json:"-"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type ReviewContext struct {
	Config            Config                `json:"config"`
	Credentials       CredentialRevisions   `json:"credentials"`
	Postings          []Posting             `json:"postings"`
	ProductMappings   []ProductMappingRef   `json:"product_mappings"`
	WarehouseMappings []WarehouseMappingRef `json:"warehouse_mappings"`
	Correlations      []Correlation         `json:"correlations"`
	ActivePostings    map[string]State      `json:"active_postings,omitempty"`
	Metadata          MetadataRegistry      `json:"metadata"`
}

type PreviewRequest struct {
	Scope            Scope    `json:"scope"`
	ExpectedRevision int64    `json:"expected_revision"`
	PostingNumbers   []string `json:"posting_numbers,omitempty"`
	Actions          []Action `json:"actions,omitempty"`
	Actor            string   `json:"-"`
}

type Approval struct {
	Scope          Scope    `json:"scope"`
	PreviewID      string   `json:"preview_id"`
	PostingNumbers []string `json:"posting_numbers,omitempty"`
	Actor          string   `json:"-"`
	IdempotencyKey string   `json:"idempotency_key"`
	Automatic      bool     `json:"-"`
}

type PageRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Search string `json:"search,omitempty"`
}

type PostingPage struct {
	Postings   []Posting `json:"postings"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type HistoryPage struct {
	Operations []Operation `json:"operations"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// Checkpoint is a local traversal position, not a provider update watermark.
type Checkpoint struct {
	Scope          Scope               `json:"scope"`
	ConfigRevision int64               `json:"config_revision"`
	Credentials    CredentialRevisions `json:"credentials"`
	Stream         string              `json:"stream"`
	RefreshKey     string              `json:"refresh_key"`
	Revision       int64               `json:"revision"`
	Cursor         string              `json:"cursor"`
	WindowFrom     time.Time           `json:"window_from"`
	WindowTo       time.Time           `json:"window_to"`
	Complete       bool                `json:"complete"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

// SourcePage commits observations and traversal progress in one transaction.
// A crash cannot advance a checkpoint without persisting all accepted facts.
type SourcePage struct {
	Scope                      Scope               `json:"scope"`
	ConfigRevision             int64               `json:"config_revision"`
	Credentials                CredentialRevisions `json:"credentials"`
	Stream                     string              `json:"stream"`
	RefreshKey                 string              `json:"refresh_key"`
	ExpectedCheckpointRevision int64               `json:"expected_checkpoint_revision"`
	ExpectedCursor             string              `json:"expected_cursor"`
	NextCursor                 string              `json:"next_cursor"`
	WindowFrom                 time.Time           `json:"window_from"`
	WindowTo                   time.Time           `json:"window_to"`
	Complete                   bool                `json:"complete"`
	Postings                   []Posting           `json:"postings"`
	Secrets                    []PostingSecret     `json:"-"`
	StartedAt                  time.Time           `json:"started_at"`
	FinishedAt                 time.Time           `json:"finished_at"`
	IdempotencyKey             string              `json:"idempotency_key"`
	FenceToken                 string              `json:"-"`
	FreshDetail                bool                `json:"fresh_detail"`
}

type IngestionClaimRequest struct {
	Scope      Scope
	Stream     string
	RefreshKey string
	WorkerID   string
	Now        time.Time
	LeaseUntil time.Time
}

type IngestionLease struct {
	Scope          Scope
	Stream         string
	RefreshKey     string
	Token          string
	Sequence       int64
	ConfigRevision int64
	Credentials    CredentialRevisions
	LeaseUntil     time.Time
}

type ClaimRequest struct {
	WorkerID   string
	Now        time.Time
	LeaseUntil time.Time
}

// Begin journals the exact intent before network I/O. Every check includes the
// claim token, current config, mappings, credentials, grant and per-posting fence.
type DispatchGuard struct {
	Work  Work
	Now   time.Time
	Begin bool
}

// Completion is a state+claim-token CAS. Unknown and acknowledged work may only
// schedule reads. Reconciled persists correlation and outcome atomically.
type Completion struct {
	WorkID          string
	Scope           Scope
	ClaimToken      string
	ExpectedState   State
	State           State
	Code            string
	Receipt         Receipt
	Correlation     *Correlation
	ObservedTarget  *TargetObservation
	ObservedPosting *Posting
	NextRunAt       time.Time
	Now             time.Time
}

type Configurer interface {
	OrderConfig(context.Context, Scope) (Config, error)
	SaveOrderConfig(context.Context, SaveConfigRequest, time.Time) (Config, error)
	DueOrderConfigs(context.Context, time.Time, int) ([]Config, error)
}

type Reviewer interface {
	OrderReviewContext(context.Context, Scope) (ReviewContext, error)
	SaveOrderPreview(context.Context, Preview) (Preview, error)
	OrderPreview(context.Context, Scope, string) (Preview, error)
}

type Approver interface {
	ApproveOrderPreview(context.Context, Approval, time.Time) (Operation, error)
}

type Querier interface {
	OrderPostings(context.Context, Scope, PageRequest) (PostingPage, error)
	OrderHistory(context.Context, Scope, PageRequest) (HistoryPage, error)
}

type Ingestor interface {
	OrderCheckpoint(context.Context, Scope, string) (Checkpoint, error)
	CommitOrderPage(context.Context, SourcePage) (Checkpoint, error)
}

// IngestionPageStore is the consumer port for atomic page+checkpoint commits.
type IngestionPageStore interface {
	Ingestor
}

// IngestionClaimer fences a serial scope collection before provider reads. Its
// local Sequence orders collections only; it is never a provider watermark.
type IngestionClaimer interface {
	ClaimOrderIngestion(context.Context, IngestionClaimRequest) (*IngestionLease, error)
	RenewOrderIngestion(context.Context, IngestionLease, time.Time) (bool, error)
	ReleaseOrderIngestion(context.Context, IngestionLease) error
}

type SecretStore interface {
	SaveOrderSecret(context.Context, PostingSecret) error
	OrderSecret(context.Context, Scope, string, int64) (PostingSecret, error)
	DeleteExpiredOrderSecrets(context.Context, time.Time, int) (int, error)
}

type MetadataRegistrar interface {
	OrderMetadata(context.Context, Scope) (MetadataRegistry, error)
	SaveOrderMetadata(context.Context, MetadataRegistry, int64) (MetadataRegistry, error)
}

type MetadataCreationRequest struct {
	Scope          Scope
	UniqueCode     string
	Type           string
	Actor          string
	IdempotencyKey string
}

type MetadataCreationIntent struct {
	ID                string    `json:"id"`
	Scope             Scope     `json:"scope"`
	UniqueCode        string    `json:"unique_code"`
	Type              string    `json:"type"`
	Marker            string    `json:"marker"`
	State             State     `json:"state"`
	AttributeID       string    `json:"attribute_id,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key"`
	CreatedAt         time.Time `json:"created_at"`
	DispatchPermitted bool      `json:"-"`
}

type MetadataCreationCompletion struct {
	IntentID      string
	Scope         Scope
	ExpectedState State
	State         State
	AttributeID   string
	ObservedAt    time.Time
}

// MetadataCreator journals creation separately from the observed UUID registry.
// Unknown creation outcomes require exact-marker readback before another write.
type MetadataCreator interface {
	BeginOrderMetadataCreation(context.Context, MetadataCreationRequest, time.Time) (MetadataCreationIntent, error)
	CompleteOrderMetadataCreation(context.Context, MetadataCreationCompletion) (MetadataRegistry, error)
}

type Claimer interface {
	ClaimOrderWork(context.Context, ClaimRequest) (*Work, error)
}

type Guarder interface {
	GuardOrderDispatch(context.Context, DispatchGuard) (bool, error)
}

type Completer interface {
	CompleteOrderWork(context.Context, Completion) error
}

type ServiceStore interface {
	Configurer
	Reviewer
	Approver
	Querier
}

type RunnerStore interface {
	Claimer
	Guarder
	Completer
}

type Reader interface {
	ReadOrderPosting(context.Context, Scope, string) (Posting, error)
	ReadOrderCorrelation(context.Context, Correlation) (Correlation, error)
}

type Writer interface {
	WriteOrder(context.Context, Command) (Receipt, error)
}

type IntentBinder interface {
	BindOrderIntent(Command, func(context.Context) (bool, error)) error
}

type Poller interface {
	PollOrder(context.Context, Command, Receipt) (Receipt, error)
}

type Provider interface {
	Reader
	Writer
	IntentBinder
}

type Binding struct {
	Scope              Scope
	Provider           string
	Account            string
	CredentialRevision int64
	Client             Provider
}

type Resolver interface {
	ResolveOrder(context.Context, Scope, string, string, int64) (Binding, error)
}
