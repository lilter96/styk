// Package ordercorrelation evaluates identity evidence without external calls.
package ordercorrelation

import (
	"sort"
	"strings"

	"example.com/styk/internal/domain"
)

// verifiedIdentity is sealed: no current connector implements it. An arbitrary
// administrative assertion cannot establish cross-system identity.
type verifiedIdentity interface {
	verifies(posting, customerOrder string) bool
	reference() string
}

type Input struct {
	Correlation      domain.OrderCorrelation
	MappingsComplete bool
	MappingsConflict bool
	Candidates       []domain.OrderCorrelationCandidate
}

// Evaluate treats product/warehouse matches as supporting evidence only.
// NOT_FOUND is restricted to the selected traversal, never global absence.
func Evaluate(in Input, proof verifiedIdentity) domain.OrderCorrelation {
	r := in.Correlation
	r.Candidates = append([]domain.OrderCorrelationCandidate{}, in.Candidates...)
	sort.Slice(r.Candidates, func(i, j int) bool { return r.Candidates[i].OrderID < r.Candidates[j].OrderID })
	r.Reasons = []string{"SOURCE_BASELINE_INCLUSION_UNKNOWN", "UNASSIGNED_RESERVE_COVERAGE_UNKNOWN"}
	if in.MappingsConflict {
		r.Status = domain.OrderConflict
		r.Reasons = append(r.Reasons, "MAPPING_CONFLICT")
		return r
	}
	for _, candidate := range r.Candidates {
		if candidate.HistoryConflict {
			r.Status = domain.OrderConflict
			r.Reasons = append(r.Reasons, "ORDER_OBSERVATIONS_CONFLICT_OR_UNORDERED")
			return r
		}
	}
	if proof != nil && strings.TrimSpace(proof.reference()) != "" {
		matches := 0
		for _, c := range r.Candidates {
			if proof.verifies(r.PostingNumber, c.OrderID) {
				matches++
			}
		}
		if matches == 1 {
			r.Status = domain.OrderIdentityConfirmed
			r.Reasons = append(r.Reasons, "IDENTITY_SOURCE:"+proof.reference())
			return r
		}
		if matches > 1 {
			r.Status = domain.OrderConflict
			r.Reasons = append(r.Reasons, "IDENTITY_EVIDENCE_CONFLICT")
			return r
		}
	}
	switch {
	case !in.MappingsComplete:
		r.Status = domain.OrderInsufficient
		r.Reasons = append(r.Reasons, "CONFIRMED_MAPPING_OR_WAREHOUSE_MISSING")
	case len(r.Candidates) == 0:
		r.Status = domain.OrderNotFound
		r.Reasons = append(r.Reasons, "NO_CANDIDATE_IN_SELECTED_WINDOW")
	case len(r.Candidates) == 1:
		r.Status = domain.OrderCandidateOnly
		r.Reasons = append(r.Reasons, "CROSS_SYSTEM_IDENTITY_UNVERIFIED")
	default:
		r.Status = domain.OrderAmbiguous
		r.Reasons = append(r.Reasons, "MULTIPLE_CANDIDATES")
	}
	return r
}
