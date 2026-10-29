package domain

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// MappingEvidenceClass describes what supports an association, not whether it is approved.
type MappingEvidenceClass string

const (
	MappingDeterministicCandidate MappingEvidenceClass = "DETERMINISTIC_CANDIDATE"
	MappingOperatorAttested       MappingEvidenceClass = "OPERATOR_ATTESTED"
	MappingIndependentlyVerified  MappingEvidenceClass = "INDEPENDENTLY_VERIFIED"
)

// MappingReviewRequest contains only the operator's selected identities and desired class.
// Import revisions and the session identity are provided by trusted server code.
type MappingReviewRequest struct {
	Tenant, OzonAccount, MoyskladAccount string
	ProductID, TargetKind, TargetID      string
	Evidence                             MappingEvidenceClass
	SessionID                            string
	Lifetime                             time.Duration
}

type MappingReviewResult struct {
	Review          MappingReview
	Status          string
	MappingConflict bool
	PrincipalType   string
}

// MappingConfirmationRequest is constructed by the authenticated server. The
// session audit ID is not a human identity and is never supplied by a client.
type MappingConfirmationRequest struct {
	ReviewID, SessionID, IdempotencyKey string
	Acknowledged                        bool
	Explanation                         string
}

type MappingConfirmationResult struct {
	MappingID        int64                `json:"mapping_id"`
	MappingVersion   int                  `json:"mapping_version"`
	ReviewID         string               `json:"review_id"`
	EvidenceClass    MappingEvidenceClass `json:"evidence_class"`
	ConfirmedAt      time.Time            `json:"confirmed_at"`
	IdempotentReplay bool                 `json:"idempotent_replay"`
}

var ErrInvalidMappingConfirmation = errors.New("invalid mapping confirmation request")

// Validate checks syntax only. Acknowledgement is checked after an existing
// idempotency outcome so reuse of a key with different content is detected.
func (r MappingConfirmationRequest) Validate() error {
	if len(r.ReviewID) != 32 || strings.ToLower(r.ReviewID) != r.ReviewID {
		return ErrInvalidMappingConfirmation
	}
	if _, err := hex.DecodeString(r.ReviewID); err != nil {
		return ErrInvalidMappingConfirmation
	}
	if r.SessionID == "" || len(r.SessionID) > 128 || len(r.IdempotencyKey) == 0 || len(r.IdempotencyKey) > 128 || len(r.Explanation) > 1000 || !utf8.ValidString(r.Explanation) || strings.ContainsRune(r.Explanation, 0) {
		return ErrInvalidMappingConfirmation
	}
	for _, c := range r.IdempotencyKey {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("._:-", c) {
			return ErrInvalidMappingConfirmation
		}
	}
	return nil
}

// MappingReview identifies the exact local imports reviewed. It does not prove identity.
type MappingReview struct {
	ID                     string
	Tenant                 string
	Source                 ProductIdentity
	Target                 ProductIdentity
	OzonImportID           int64
	MoyskladImportID       int64
	ExistingMappingVersion *int
	EvidenceClass          MappingEvidenceClass
	CreatedAt              time.Time
	ExpiresAt              time.Time
}

// Validate checks a review's structural integrity. It does not verify ownership,
// source revisions, or the truth of a proposed relationship.
func (r MappingReview) Validate() error {
	validText := func(s string) bool { return s != "" && s == strings.TrimSpace(s) }
	if !validText(r.ID) || !validText(r.Tenant) || !validText(r.Source.Account) || !validText(r.Source.ID) || !validText(r.Target.Account) || !validText(r.Target.ID) {
		return errors.New("review identity and scope are required")
	}
	if r.Source.System != "ozon" || r.Source.Kind != "product" || r.Target.System != "moysklad" || (r.Target.Kind != "product" && r.Target.Kind != "variant") {
		return errors.New("invalid external identity kind")
	}
	if r.OzonImportID <= 0 || r.MoyskladImportID <= 0 || r.CreatedAt.IsZero() || (!r.ExpiresAt.After(r.CreatedAt) || r.ExpiresAt.Sub(r.CreatedAt) > 10*time.Minute) || (r.ExistingMappingVersion != nil && *r.ExistingMappingVersion <= 0) {
		return errors.New("invalid revision or review lifetime")
	}
	// The current system has no independently verifiable identity source.
	if r.EvidenceClass != MappingDeterministicCandidate && r.EvidenceClass != MappingOperatorAttested {
		return errors.New("unsupported or unverified evidence class")
	}
	return nil
}
