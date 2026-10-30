package orders

import (
	"context"
	"time"
)

type ActionPackage struct {
	Lines []ActionQuantity `json:"lines,omitempty"`
}

type ActionQuantity struct {
	Identity LineIdentity `json:"identity,omitempty"`
	Quantity Decimal      `json:"quantity"`
}

const (
	ActionPostingSplit         Action = "posting_split"
	ActionPostingPartialCancel Action = "posting_partial_cancel"
	ActionPostingTracking      Action = "posting_tracking"
	ActionPostingLastMile      Action = "posting_last_mile"
	ActionPostingSentBySeller  Action = "posting_sent_by_seller"
	ActionPostingLabelCreate   Action = "posting_label_create"
	ActionDemandCreate         Action = "demand_create"
	ActionSalesReturnCreate    Action = "salesreturn_create"
)

// Private strings are accepted from the authenticated operator but sealed before
// a preview or intent is persisted. Public flags disclose presence, never values.
type ActionParameters struct {
	Packages               []ActionPackage  `json:"packages,omitempty"`
	Lines                  []ActionQuantity `json:"lines,omitempty"`
	ReasonID               string           `json:"reason_id,omitempty"`
	ReasonMessage          string           `json:"reason_message,omitempty"`
	TrackingNumber         string           `json:"tracking_number,omitempty"`
	EventReference         string           `json:"event_reference,omitempty"`
	EventConfirmed         bool             `json:"event_confirmed,omitempty"`
	ReadinessConfirmed     bool             `json:"readiness_confirmed,omitempty"`
	LabelType              string           `json:"label_type,omitempty"`
	ReasonMessageProvided  bool             `json:"reason_message_provided,omitempty"`
	TrackingProvided       bool             `json:"tracking_provided,omitempty"`
	EventReferenceProvided bool             `json:"event_reference_provided,omitempty"`
}

type ActionProposal struct {
	PostingNumber string           `json:"posting_number"`
	Action        Action           `json:"action"`
	Parameters    ActionParameters `json:"parameters"`
}

type ProtectedActionInput struct {
	Reference string `json:"reference"`
	Revision  int64  `json:"revision"`
}

type ActionTask struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type OrderArtifact struct {
	Available   bool   `json:"available"`
	DownloadURL string `json:"download_url,omitempty"`
	LabelType   string `json:"label_type,omitempty"`
}

type CustomerReader interface {
	Customer(context.Context, Scope, string, ProtectedCustomer) (*Customer, error)
}

type ActionInputReader interface {
	ReadOrderActionInput(context.Context, Scope, string, Action, ProtectedActionInput) (ActionParameters, error)
}

type ActionInputProtector interface {
	ProtectOrderActionInput(context.Context, Config, string, Action, ActionParameters, time.Time) (ActionParameters, *ProtectedActionInput, error)
}

type ActionSecret struct {
	Scope Scope
	// PostingNumber is the Ozon posting number associated with this secret.
	PostingNumber        string
	Action               Action
	Reference            string
	ConfigRevision       int64
	Ciphertext           []byte
	CreatedAt, ExpiresAt time.Time
}

type ActionSecretStore interface {
	SaveOrderActionSecret(context.Context, ActionSecret) (ActionSecret, error)
	OrderActionSecret(context.Context, Scope, string, Action, ProtectedActionInput) (ActionSecret, error)
}

type ActionReason struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ActionReasonReader interface {
	ReadOrderActionReasons(context.Context, Scope, string) ([]ActionReason, error)
}

// LabelTask is the bounded provider task result. A completed task carries one
// validated provider PDF URL; the URL is sealed before durable persistence.
type LabelTask struct {
	ID                   string `json:"id"`
	Code                 string `json:"code"`
	FileURL              string `json:"-"`
	PostingsCount        int    `json:"postings_count"`
	PrintedPostingsCount int    `json:"printed_postings_count"`
	ErrorCode            string `json:"error_code,omitempty"`
	ErrorMessage         string `json:"error_message,omitempty"`
}

// LabelTaskReader is the bounded provider task reader. The URL of a completed
// task is sealed before durable persistence.
type LabelTaskReader interface {
	ReadOrderLabelTask(context.Context, Scope, string) (LabelTask, error)
}

// ActionOption describes one reviewed provider operation for an exact posting.
// Blockers use the same codes as preview validation so the operator can inspect
// the authoritative reason before a proposal is created.
type ActionOption struct {
	Action             Action       `json:"action"`
	Allowed            bool         `json:"allowed"`
	Blockers           []Validation `json:"blockers"`
	ParametersRequired []string     `json:"parameters_required"`
	Endpoint           string       `json:"endpoint,omitempty"`
	ExpectedStatus     string       `json:"expected_status,omitempty"`
}

type ActionOptions struct {
	Scope               Scope          `json:"scope"`
	PostingNumber       string         `json:"posting_number"`
	SourceRevision      string         `json:"source_revision"`
	Actions             []ActionOption `json:"actions"`
	CancellationReasons []ActionReason `json:"cancellation_reasons"`
	LabelTypes          []string       `json:"label_types"`
}

// ActionOptionsRequest is the request type for ActionOptions.
type ActionOptionsRequest struct {
	Scope         Scope  `json:"scope"`
	PostingNumber string `json:"posting_number"`
}

func (input ActionParameters) HasPrivateValues() bool {
	return input.ReasonMessage != "" || input.TrackingNumber != "" || input.EventReference != ""
}

func (input ActionParameters) Public() ActionParameters {
	input.ReasonMessageProvided = input.ReasonMessage != ""
	input.TrackingProvided = input.TrackingNumber != ""
	input.EventReferenceProvided = input.EventReference != ""
	input.ReasonMessage, input.TrackingNumber, input.EventReference = "", "", ""
	return input
}
