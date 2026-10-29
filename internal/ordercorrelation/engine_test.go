package ordercorrelation

import (
	"testing"

	"github.com/lilter96/styk/internal/domain"
)

type syntheticIdentity struct{ posting, order, ref string }

func (s syntheticIdentity) verifies(p, o string) bool { return p == s.posting && o == s.order }
func (s syntheticIdentity) reference() string         { return s.ref }

func TestCorrelationNeverConfirmsCoincidentalCandidates(t *testing.T) {
	base := domain.OrderCorrelation{PostingNumber: "posting"}
	for _, tc := range []struct {
		name string
		in   Input
		want domain.OrderCorrelationStatus
	}{
		{"no candidate in window", Input{Correlation: base, MappingsComplete: true}, domain.OrderNotFound},
		{"missing mapping", Input{Correlation: base}, domain.OrderInsufficient},
		{"one candidate", Input{Correlation: base, MappingsComplete: true, Candidates: []domain.OrderCorrelationCandidate{{OrderID: "order-a"}}}, domain.OrderCandidateOnly},
		{"ambiguity", Input{Correlation: base, MappingsComplete: true, Candidates: []domain.OrderCorrelationCandidate{{OrderID: "order-b"}, {OrderID: "order-a"}}}, domain.OrderAmbiguous},
		{"mapping conflict", Input{Correlation: base, MappingsConflict: true}, domain.OrderConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.in, nil)
			if got.Status != tc.want {
				t.Fatalf("got %s want %s", got.Status, tc.want)
			}
			if len(got.Reasons) < 2 || got.Reasons[0] != "SOURCE_BASELINE_INCLUSION_UNKNOWN" {
				t.Fatal("baseline uncertainty lost")
			}
			if len(got.Candidates) > 1 && got.Candidates[0].OrderID != "order-a" {
				t.Fatal("candidates not deterministic")
			}
		})
	}
}

func TestSyntheticExplicitIdentityDoesNotProveReserveInclusion(t *testing.T) {
	in := Input{Correlation: domain.OrderCorrelation{PostingNumber: "posting"}, MappingsComplete: true, Candidates: []domain.OrderCorrelationCandidate{{OrderID: "order"}}}
	got := Evaluate(in, syntheticIdentity{"posting", "order", "synthetic-verified-source"})
	if got.Status != domain.OrderIdentityConfirmed || got.Reasons[0] != "SOURCE_BASELINE_INCLUSION_UNKNOWN" {
		t.Fatalf("synthetic identity collapsed into stock proof: %+v", got)
	}
	if unverified := Evaluate(in, nil); unverified.Status != domain.OrderCandidateOnly {
		t.Fatal("ordinary coincidence confirmed identity")
	}
}
