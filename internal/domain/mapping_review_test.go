package domain

import (
	"testing"
	"time"
)

func TestMappingReviewDoesNotInventIndependentVerification(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := MappingReview{ID: "review", Tenant: "tenant", Source: ProductIdentity{System: "ozon", Account: "o", Kind: "product", ID: "p"}, Target: ProductIdentity{System: "moysklad", Account: "m", Kind: "variant", ID: "v"}, OzonImportID: 1, MoyskladImportID: 2, CreatedAt: now, ExpiresAt: now.Add(time.Minute), EvidenceClass: MappingOperatorAttested}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*MappingReview)
	}{
		{"independent evidence unavailable", func(r *MappingReview) { r.EvidenceClass = MappingIndependentlyVerified }},
		{"missing tenant", func(r *MappingReview) { r.Tenant = "" }},
		{"invalid target kind", func(r *MappingReview) { r.Target.Kind = "warehouse" }},
		{"missing revision", func(r *MappingReview) { r.OzonImportID = 0 }},
		{"invalid lifetime", func(r *MappingReview) { r.ExpiresAt = now }},
		{"invalid mapping version", func(r *MappingReview) { v := 0; r.ExistingMappingVersion = &v }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if r.Validate() == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
}
