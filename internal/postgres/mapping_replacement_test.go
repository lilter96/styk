package postgres

import (
	"testing"

	"github.com/lilter96/styk/internal/domain"
)

func TestReplacementRequiresExactChangedMappingInSameAccount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*reviewSnapshot, *domain.MappingReview)
		want   bool
	}{
		{"changed target", func(*reviewSnapshot, *domain.MappingReview) {}, true},
		{"missing mapping", func(s *reviewSnapshot, _ *domain.MappingReview) { s.MappingID = nil }, false},
		{"missing version", func(s *reviewSnapshot, _ *domain.MappingReview) { s.MappingVersion = nil }, false},
		{"unbound review", func(_ *reviewSnapshot, r *domain.MappingReview) { r.ExistingMappingVersion = nil }, false},
		{"changed version", func(s *reviewSnapshot, _ *domain.MappingReview) { v := 2; s.MappingVersion = &v }, false},
		{"other account", func(s *reviewSnapshot, _ *domain.MappingReview) { s.MappingTargetAccount = "other" }, false},
		{"occupied offer", func(s *reviewSnapshot, _ *domain.MappingReview) { s.OfferConflict = true }, false},
		{"no change", func(s *reviewSnapshot, r *domain.MappingReview) { s.MappingTarget = r.Target.ID }, false},
		{"changed offer", func(s *reviewSnapshot, r *domain.MappingReview) {
			s.MappingTarget = r.Target.ID
			s.MappingOffer = "old"
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, version := int64(1), 1
			s := reviewSnapshot{MappingID: &id, MappingVersion: &version, MappingTargetAccount: "m", MappingKind: "product", MappingTarget: "old", MappingOffer: "offer", Offer: "offer"}
			r := domain.MappingReview{ExistingMappingVersion: &version, Target: domain.ProductIdentity{Account: "m", Kind: "product", ID: "new"}}
			tc.mutate(&s, &r)
			if got := replacementAllowed(s, r); got != tc.want {
				t.Fatalf("replacement allowed=%v, want %v", got, tc.want)
			}
		})
	}
}
