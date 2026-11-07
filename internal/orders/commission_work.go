package orders

import "time"

type CommissionPreview struct {
	Analysis      *FinancialLedger      `json:"analysis,omitempty"`
	ID            string                `json:"id"`
	Plan          CommissionPlan        `json:"plan"`
	Validation    []Validation          `json:"validation"`
	ExistingWork  *CommissionWork       `json:"existing_work,omitempty"`
	SourceChanged bool                  `json:"source_changed,omitempty"`
	SourceDiff    *CommissionSourceDiff `json:"source_diff,omitempty"`
	Actor         string                `json:"-"`
	Automatic     bool                  `json:"-"`
}

type CommissionWork struct {
	ID             string              `json:"id"`
	Scope          Scope               `json:"scope"`
	Plan           CommissionPlan      `json:"plan"`
	PreviewID      string              `json:"preview_id"`
	ConfigRevision int64               `json:"config_revision"`
	Credentials    CredentialRevisions `json:"credentials"`
	State          State               `json:"state"`
	Code           string              `json:"code,omitempty"`
	Receipt        Receipt             `json:"receipt"`
	DocumentID     string              `json:"document_id,omitempty"`
	DispatchCount  int                 `json:"dispatch_count"`
	Automatic      bool                `json:"automatic"`
	NextRunAt      time.Time           `json:"next_run_at"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	SourceChanged  bool                `json:"source_changed,omitempty"`
	ClaimToken     string              `json:"-"`
	LeaseUntil     time.Time           `json:"-"`
}

type CommissionApproval struct {
	Scope          Scope  `json:"scope"`
	PreviewID      string `json:"preview_id"`
	Actor          string `json:"-"`
	IdempotencyKey string `json:"idempotency_key"`
	Automatic      bool   `json:"-"`
	RetryWorkID    string `json:"retry_work_id,omitempty"`
}

type CommissionCompletion struct {
	WorkID        string
	Scope         Scope
	ClaimToken    string
	ExpectedState State
	State         State
	Code          string
	Receipt       Receipt
	Observation   *CommissionObservation
	SourceChanged bool
	NextRunAt     time.Time
	Now           time.Time
}
