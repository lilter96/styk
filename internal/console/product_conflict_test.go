package console

import (
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
)

func TestProductConflictReasonsDescribeEvidence(t *testing.T) {
	t.Parallel()
	target := domain.ProductIdentity{System: "moysklad", Account: "m", Kind: "product", ID: "current"}
	tests := []struct {
		name   string
		result domain.MatchResult
		want   []string
	}{
		{"shared stock is not conflict", domain.MatchResult{Status: matching.AlreadyConfirmed, SharedInventory: true}, nil},
		{"offer collision", domain.MatchResult{Status: matching.MappingConflict}, []string{"OFFER_CONFLICT"}},
		{"another target", domain.MatchResult{Status: matching.MappingConflict, Source: domain.MatchSource{OfferID: "offer"}, Confirmed: &domain.Mapping{Target: target, OfferID: "offer"}, Candidates: []domain.MatchCandidate{{Target: domain.ProductIdentity{Account: "m", Kind: "product", ID: "different"}}}}, []string{"DIFFERENT_CANDIDATE"}},
		{"changed offer", domain.MatchResult{Status: matching.MappingConflict, Source: domain.MatchSource{OfferID: "new"}, Confirmed: &domain.Mapping{Target: target, OfferID: "old"}}, []string{"OFFER_CHANGED"}},
		{"different account", domain.MatchResult{Status: matching.MappingConflict, Confirmed: &domain.Mapping{Target: domain.ProductIdentity{Account: "other"}}}, []string{"OTHER_ACCOUNT"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := productConflictReasons(tc.result, "m"); !slices.Equal(got, tc.want) {
				t.Fatalf("reasons=%v, want %v", got, tc.want)
			}
		})
	}
}
