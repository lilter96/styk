// Package catalogsync orchestrates reviewed catalog changes through the existing outbox.
// Store implementations own transactions; no transaction may span provider I/O.
package catalogsync

import (
	"context"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

var (
	ErrInvalid  = errors.New("invalid catalog synchronization request")
	ErrNotFound = errors.New("catalog synchronization resource not found")
	ErrConflict = errors.New("catalog synchronization idempotency conflict")
	ErrStale    = errors.New("catalog synchronization evidence changed")
	ErrBlocked  = errors.New("catalog synchronization is blocked")
)

type Config struct {
	Scope           catalog.Scope    `json:"scope"`
	Revision        int64            `json:"revision"`
	Enabled         bool             `json:"enabled"`
	TestEnabled     bool             `json:"test_enabled"`
	IntervalSeconds int64            `json:"interval_seconds"`
	Rules           catalog.Policies `json:"rules"`
	Targets         []Enrollment     `json:"targets"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// Enrollment names either an existing confirmed mapping or an explicit creation.
// AutomaticFields is the exact field allowlist previously authorized by the actor.
type Enrollment struct {
	ID              string           `json:"id"`
	MappingID       string           `json:"mapping_id,omitempty"`
	MappingVersion  int64            `json:"mapping_version,omitempty"`
	Source          catalog.Identity `json:"source"`
	Target          catalog.Identity `json:"target"`
	Create          *Creation        `json:"create,omitempty"`
	Automatic       bool             `json:"automatic"`
	AutomaticFields []catalog.Field  `json:"automatic_fields,omitempty"`
}

type Creation struct {
	ID           string                          `json:"id,omitempty"`
	ExternalCode string                          `json:"external_code,omitempty"`
	Values       map[catalog.Field]catalog.Value `json:"values"`
	Extensions   map[string]catalog.Value        `json:"extensions,omitempty"`
	Parent       *catalog.Identity               `json:"parent,omitempty"`
}

type SaveConfigRequest struct {
	Config           Config `json:"config"`
	ExpectedRevision int64  `json:"expected_revision"`
	Actor            string `json:"-"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type CredentialRevisions struct {
	Moysklad int64 `json:"moysklad"`
	Ozon     int64 `json:"ozon"`
}

// TargetContext is authoritative store evidence, never supplied by the browser.
// CreatedTarget survives restarts and replaces a creation enrollment's empty ID.
type TargetContext struct {
	Enrollment    Enrollment        `json:"enrollment"`
	Mapping       *catalog.Mapping  `json:"mapping,omitempty"`
	Baseline      *catalog.Baseline `json:"baseline,omitempty"`
	CreatedTarget *catalog.Identity `json:"created_target,omitempty"`
	Fenced        bool              `json:"fenced"`
}

type ReviewContext struct {
	Config      Config              `json:"config"`
	Credentials CredentialRevisions `json:"credentials"`
	Targets     []TargetContext     `json:"targets"`
}

type PreviewRequest struct {
	Scope            catalog.Scope                              `json:"scope"`
	ExpectedRevision int64                                      `json:"expected_revision"`
	EnrollmentIDs    []string                                   `json:"enrollment_ids,omitempty"`
	ManualValues     map[string]map[catalog.Field]catalog.Value `json:"manual_values,omitempty"`
	Actor            string                                     `json:"-"`
}

// Decision preserves mapping order; Commands may point in either direction when
// field authorities differ. A conflicting product is never partially approved.
type Decision struct {
	EnrollmentID string               `json:"enrollment_id"`
	Mapping      *catalog.Mapping     `json:"mapping,omitempty"`
	Source       catalog.Product      `json:"source"`
	Target       catalog.Product      `json:"target"`
	TargetAbsent bool                 `json:"target_absent"`
	Baseline     *catalog.Baseline    `json:"baseline,omitempty"`
	Plan         catalog.Plan         `json:"plan"`
	Commands     []catalog.Command    `json:"commands"`
	Validation   []catalog.FieldError `json:"validation"`
	Ready        bool                 `json:"ready"`
}

type Preview struct {
	ID             string              `json:"id"`
	Scope          catalog.Scope       `json:"scope"`
	ConfigRevision int64               `json:"config_revision"`
	Credentials    CredentialRevisions `json:"credentials"`
	Decisions      []Decision          `json:"decisions"`
	Actor          string              `json:"-"`
	Automatic      bool                `json:"automatic"`
	CreatedAt      time.Time           `json:"created_at"`
	ExpiresAt      time.Time           `json:"expires_at"`
}

// Approval selects whole reviewed products, not new commands or field values.
type Approval struct {
	Scope          catalog.Scope `json:"scope"`
	PreviewID      string        `json:"preview_id"`
	EnrollmentIDs  []string      `json:"enrollment_ids,omitempty"`
	Actor          string        `json:"-"`
	IdempotencyKey string        `json:"idempotency_key"`
	Automatic      bool          `json:"-"`
}

type State string

const (
	StateQueued       State = "QUEUED"
	StateClaimed      State = "CLAIMED"
	StateDispatching  State = "DISPATCHING"
	StateProcessing   State = "PROCESSING"
	StateAcknowledged State = "ACKNOWLEDGED"
	StateUnknown      State = "UNKNOWN"
	StateReconciled   State = "RECONCILED"
	StateRejected     State = "REJECTED"
	StateBlocked      State = "BLOCKED"
)

type Operation struct {
	ID        string        `json:"id"`
	Scope     catalog.Scope `json:"scope"`
	PreviewID string        `json:"preview_id"`
	State     State         `json:"state"`
	Targets   []Work        `json:"targets"`
	CreatedAt time.Time     `json:"created_at"`
}

// Work always holds exactly one frozen provider command. ClaimToken is an outbox
// fencing token. Recovery may reclaim reads; expired DISPATCHING becomes UNKNOWN.
type Work struct {
	ID                 string              `json:"id"`
	OperationID        string              `json:"operation_id"`
	Scope              catalog.Scope       `json:"scope"`
	EnrollmentID       string              `json:"enrollment_id"`
	ConfigRevision     int64               `json:"config_revision"`
	Credentials        CredentialRevisions `json:"credentials"`
	Command            catalog.Command     `json:"command"`
	CommandFingerprint string              `json:"command_fingerprint"`
	Decision           Decision            `json:"decision"`
	State              State               `json:"state"`
	Receipt            catalog.Receipt     `json:"receipt"`
	CreatedTarget      *catalog.Identity   `json:"created_target,omitempty"`
	Automatic          bool                `json:"automatic"`
	ClaimToken         string              `json:"-"`
	NextRunAt          time.Time           `json:"next_run_at"`
	Code               string              `json:"code,omitempty"`
}

type ClaimRequest struct {
	WorkerID   string
	Now        time.Time
	LeaseUntil time.Time
}

// DispatchGuard is checked under store locks against config, confirmed mapping,
// credential revisions, target fence and claim token. Begin durably journals the
// exact intent and transitions CLAIMED -> DISPATCHING before any network write.
type DispatchGuard struct {
	Work  Work
	Now   time.Time
	Begin bool
}

// Completion is a compare-and-swap on ClaimToken and State. PROCESSING and
// ACKNOWLEDGED schedule reads only. UNKNOWN fences this target, never the account.
// RECONCILED atomically persists CreatedTarget and Baseline with the outcome.
type Completion struct {
	WorkID         string
	Scope          catalog.Scope
	ClaimToken     string
	ExpectedState  State
	State          State
	Code           string
	Receipt        catalog.Receipt
	CreatedTarget  *catalog.Identity
	Baseline       *catalog.Baseline
	ObservedSource *catalog.Product
	ObservedTarget *catalog.Product
	NextRunAt      time.Time
	Now            time.Time
}

type PageRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Search string `json:"search,omitempty"`
}

type ProductPage struct {
	Products   []catalog.Product `json:"products"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type HistoryPage struct {
	Operations []Operation `json:"operations"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type Configurer interface {
	CatalogConfig(context.Context, catalog.Scope) (Config, error)
	SaveCatalogConfig(context.Context, SaveConfigRequest, time.Time) (Config, error)
	DueCatalogConfigs(context.Context, time.Time, int) ([]Config, error)
}

type Reviewer interface {
	CatalogReviewContext(context.Context, catalog.Scope) (ReviewContext, error)
	SaveCatalogPreview(context.Context, Preview) (Preview, error)
	CatalogPreview(context.Context, catalog.Scope, string) (Preview, error)
}

// Approver must atomically revalidate preview expiry/config/mappings/credentials,
// actor+idempotency and target fences, then create operations and existing outbox
// rows. Automatic approvals additionally recheck explicit per-field enrollment.
type Approver interface {
	ApproveCatalogPreview(context.Context, Approval, time.Time) (Operation, error)
}

type Querier interface {
	CatalogProducts(context.Context, catalog.Scope, PageRequest) (ProductPage, error)
	CatalogHistory(context.Context, catalog.Scope, PageRequest) (HistoryPage, error)
}

type Claimer interface {
	ClaimCatalogWork(context.Context, ClaimRequest) (*Work, error)
}

type Guarder interface {
	GuardCatalogDispatch(context.Context, DispatchGuard) (bool, error)
}

type Completer interface {
	CompleteCatalogWork(context.Context, Completion) error
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

// IntentBinder must fail closed before dispatch and invoke guard immediately
// before the single-target request. It must not widen or batch the command.
type IntentBinder interface {
	BindCatalogIntent(catalog.Command, func(context.Context) (bool, error)) error
}

type Provider interface {
	catalog.Reader
	catalog.Writer
	IntentBinder
}

// Poller performs read-only task/status requests. Accepted != reconciled.
type Poller interface {
	PollCatalog(context.Context, catalog.Command, catalog.Receipt) (catalog.Receipt, error)
}

// Binding asserts exact tenant/account and credential revision checked by the
// resolver. Service/Runner reject a binding that differs from the frozen scope.
type Binding struct {
	Scope              catalog.Scope
	Account            catalog.Account
	CredentialRevision int64
	Client             Provider
}

type Resolver interface {
	ResolveCatalog(context.Context, catalog.Scope, catalog.Account, int64) (Binding, error)
}

type ResolveFunc func(context.Context, catalog.Scope, catalog.Account, int64) (Binding, error)

func (resolve ResolveFunc) ResolveCatalog(ctx context.Context, scope catalog.Scope, account catalog.Account, revision int64) (Binding, error) {
	return resolve(ctx, scope, account, revision)
}
