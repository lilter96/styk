package orders

import "time"

type CarriageWork struct {
	ID             string              `json:"id"`
	Scope          Scope               `json:"scope"`
	Plan           CarriagePlan        `json:"plan"`
	PreviewID      string              `json:"preview_id"`
	ConfigRevision int64               `json:"config_revision"`
	Credentials    CredentialRevisions `json:"credentials"`
	State          State               `json:"state"`
	Code           string              `json:"code,omitempty"`
	Receipt        Receipt             `json:"receipt"`
	CarriageID     string              `json:"carriage_id,omitempty"`
	DispatchCount  int                 `json:"dispatch_count"`
	Observation    *CarriageSnapshot   `json:"observation,omitempty"`
	NextRunAt      time.Time           `json:"next_run_at"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	ClaimToken     string              `json:"-"`
	LeaseUntil     time.Time           `json:"-"`
}

type CarriageApproval struct {
	Scope                      Scope  `json:"scope"`
	PreviewID                  string `json:"preview_id"`
	IdempotencyKey             string `json:"idempotency_key"`
	AllReadyConsent            bool   `json:"all_ready_consent"`
	PhysicalReadinessConfirmed bool   `json:"physical_readiness_confirmed"`
	Actor                      string `json:"-"`
}

type CarriageCompletion struct {
	WorkID        string
	Scope         Scope
	ClaimToken    string
	ExpectedState State
	State         State
	Code          string
	Receipt       Receipt
	CarriageID    string
	Observation   *CarriageSnapshot
	NextRunAt     time.Time
	Now           time.Time
}
