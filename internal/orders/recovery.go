package orders

import (
	"context"
	"strings"
	"time"
)

const RecoveryEvidenceTTL = 2 * time.Minute

type RecoveryRequest struct {
	Scope  Scope  `json:"scope"`
	WorkID string `json:"work_id"`
	Actor  string `json:"-"`
}

type RecoverySource struct {
	PostingNumber     string        `json:"posting_number"`
	CommercialOrderID string        `json:"commercial_order_id"`
	Revision          string        `json:"revision"`
	Fingerprint       string        `json:"fingerprint"`
	Status            Field[string] `json:"status"`
	ObservedAt        time.Time     `json:"observed_at"`
}

func NewRecoverySource(posting Posting) (RecoverySource, error) {
	source := RecoverySource{PostingNumber: posting.PostingNumber, CommercialOrderID: posting.CommercialOrderID, Revision: posting.Evidence.Revision, Status: posting.Status, ObservedAt: posting.Evidence.ObservedAt}
	posting.PII, posting.Customer = nil, ProtectedCustomer{}
	hash, err := PostingFingerprint(posting)
	source.Fingerprint = hash
	return source, err
}

// RecoveryTarget omits buyer data and free-text native fields. Its revision
// identifies the complete actual read without copying arbitrary provider JSON.
type RecoveryTarget struct {
	Correlation    Correlation      `json:"correlation"`
	Kind           string           `json:"kind"`
	Revision       string           `json:"revision"`
	Positions      []TargetPosition `json:"positions"`
	OrganizationID string           `json:"organization_id"`
	AgentID        string           `json:"agent_id"`
	StoreID        string           `json:"store_id"`
	StateID        string           `json:"state_id"`
	Complete       bool             `json:"complete"`
	ObservedAt     time.Time        `json:"observed_at"`
}

func NewRecoveryTarget(target TargetObservation) RecoveryTarget {
	return RecoveryTarget{Correlation: target.Correlation, Kind: "customerorder", Revision: target.Revision, Positions: target.Positions, OrganizationID: target.Fields.OrganizationID, AgentID: target.Fields.AgentID, StoreID: target.Fields.StoreID, StateID: target.Fields.StateID, Complete: target.Complete, ObservedAt: target.ObservedAt}
}

type RecoveryEvidence struct {
	ID                   string              `json:"id"`
	Scope                Scope               `json:"scope"`
	WorkID               string              `json:"work_id"`
	CommandFingerprint   string              `json:"command_fingerprint"`
	ConfigRevision       int64               `json:"config_revision"`
	Credentials          CredentialRevisions `json:"credentials"`
	StoredSourceRevision string              `json:"stored_source_revision"`
	Source               *RecoverySource     `json:"source,omitempty"`
	Target               *RecoveryTarget     `json:"target,omitempty"`
	Code                 string              `json:"code"`
	CreatedAt            time.Time           `json:"created_at"`
	ExpiresAt            time.Time           `json:"expires_at"`
	Actor                string              `json:"-"`
}

type RecoveryAttestation struct {
	ID                string    `json:"id"`
	EvidenceID        string    `json:"evidence_id"`
	CreatedAt         time.Time `json:"created_at"`
	ValidUntil        time.Time `json:"valid_until"`
	NewOperationID    string    `json:"new_operation_id,omitempty"`
	NewOperationState State     `json:"new_operation_state,omitempty"`
	NewDispatchCount  int       `json:"new_dispatch_count"`
}

type RecoveryResolution struct {
	Attestation         *RecoveryAttestation `json:"attestation,omitempty"`
	ID                  string               `json:"id"`
	Scope               Scope                `json:"scope"`
	WorkID              string               `json:"work_id"`
	EvidenceID          string               `json:"evidence_id"`
	NativeID            string               `json:"native_id"`
	NativeKind          string               `json:"native_kind"`
	AcceptedUncertainty bool                 `json:"accepted_uncertainty"`
	WorkerStopped       bool                 `json:"worker_stopped"`
	Actor               string               `json:"actor"`
	CreatedAt           time.Time            `json:"created_at"`
	NewOperationID      string               `json:"new_operation_id,omitempty"`
	ReasonCiphertext    []byte               `json:"-"`
	ReasonDigest        string               `json:"-"`
}

type RecoveryResolveRequest struct {
	RecoveryRequest
	EvidenceID       string `json:"evidence_id"`
	IdempotencyKey   string `json:"idempotency_key"`
	Reason           string `json:"reason"`
	Acknowledge      bool   `json:"acknowledge"`
	WorkerStopped    bool   `json:"worker_stopped"`
	AttestationID    string `json:"-"`
	ReasonCiphertext []byte `json:"-"`
	ReasonDigest     string `json:"-"`
}

func (r RecoveryResolveRequest) Validate() error {
	if r.Scope.Validate() != nil || !validID(r.WorkID) || !validID(r.EvidenceID) || !validID(r.IdempotencyKey) || !r.Acknowledge || !r.WorkerStopped || r.Actor == "" {
		return ErrInvalid
	}
	if len(strings.TrimSpace(r.Reason)) < 16 || len(r.Reason) > 1000 || strings.ContainsAny(r.Reason, "\x00\r") {
		return ErrInvalid
	}
	return nil
}

type RecoveryView struct {
	Work       Work                `json:"work"`
	Evidence   *RecoveryEvidence   `json:"evidence,omitempty"`
	Resolution *RecoveryResolution `json:"resolution,omitempty"`
	Eligible   bool                `json:"eligible"`
	Blockers   []Validation        `json:"blockers"`
}

type RecoveryContext struct {
	View        RecoveryView
	Config      Config
	Credentials CredentialRevisions
	Posting     Posting
}

func (w Work) RecoveryUpdateEligible() bool {
	return w.Scope == w.Command.Scope && w.Command.Validate() == nil && w.State == StateUnknown && w.DispatchCount == 1 && (w.Command.Action == ActionMirrorUpdate || w.Command.Action == ActionMirrorPositionUpdate) && validUUID(w.Command.Correlation.CustomerOrderID)
}

type RecoveryStore interface {
	OrderRecoveryContext(context.Context, RecoveryRequest) (RecoveryContext, error)
	SaveOrderRecoveryEvidence(context.Context, RecoveryEvidence) (RecoveryView, error)
	ResolveOrderRecovery(context.Context, RecoveryResolveRequest) (RecoveryView, error)
}

type RecoveryReasonSealer interface {
	SealOrderRecoveryReason(Scope, string, string, string) ([]byte, string, error)
}

// RecoveryPositionBaseline records an explicit operator attestation to the
// freshly observed intended native state; it never changes the original work.
func RecoveryPositionBaseline(work Work, evidence RecoveryEvidence, resolution RecoveryResolution, now time.Time) (PositionBaseline, error) {
	a := resolution.Attestation
	if !work.RecoveryUpdateEligible() || resolution.Scope != work.Scope || resolution.WorkID != work.ID || !resolution.AcceptedUncertainty || a == nil || a.EvidenceID != evidence.ID || !a.ValidUntil.After(now) || evidence.Target == nil || !evidence.Target.Complete || evidence.Target.Correlation != work.Command.Correlation {
		return PositionBaseline{}, ErrBlocked
	}
	target := evidence.Target
	sources := work.Command.CustomerOrder.Positions
	if work.Command.PositionPatch != nil {
		if !PositionPatchObserved(work.Command, TargetObservation{Positions: target.Positions}) {
			return PositionBaseline{}, ErrBlocked
		}
		sources = work.Command.PositionPatch.appliedSources()
	} else if !work.Command.TargetPositionsComplete || !NativePositionsEqual(work.Command.TargetPositions, target.Positions) {
		return PositionBaseline{}, ErrBlocked
	}
	baseline := PositionBaseline{Scope: work.Scope, WorkID: work.ID, CommandID: work.Command.ID, Correlation: target.Correlation, ObservedAt: a.CreatedAt, ValidUntil: a.ValidUntil, RecoveryID: a.ID, EvidenceClass: "OPERATOR_ATTESTED", Bindings: make([]PositionBinding, 0, len(sources))}
	for _, source := range sources {
		index := uniqueNativePosition(source, target.Positions)
		if index < 0 || !PositionRepresented(source, target.Positions[index]) {
			return PositionBaseline{}, ErrBlocked
		}
		baseline.Bindings = append(baseline.Bindings, PositionBinding{Source: source, Native: target.Positions[index]})
	}
	return baseline, baseline.Validate()
}
