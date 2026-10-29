package domain

import (
	"errors"
	"strings"
	"time"
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
