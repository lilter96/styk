package orders

import (
	"errors"
	"testing"
	"time"
)

func TestHybridLabelProofCannotCrossAnyTargetBoundary(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	posting := Posting{Scope: Scope{Tenant: "merchant", MoyskladAccount: "moy", OzonAccount: "ozon"}, PostingNumber: "posting", CommercialOrderID: "order", WarehouseID: Present("warehouse"), DeliveryMethodID: Present("method"), Evidence: EvidenceRevision{Origin: OriginOzon}}
	proof := LabelProof{Scope: posting.Scope, WorkID: "label-work", TaskID: "99", PostingNumber: posting.PostingNumber, CommercialOrderID: posting.CommercialOrderID, WarehouseID: "warehouse", DeliveryMethodID: "method", ExpiresAt: now.Add(time.Hour)}
	if err := proof.ValidateFor(posting, "label-work", now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*LabelProof)
	}{
		{"tenant", func(p *LabelProof) { p.Scope.Tenant = "other" }},
		{"source account", func(p *LabelProof) { p.Scope.MoyskladAccount = "other" }},
		{"destination account", func(p *LabelProof) { p.Scope.OzonAccount = "other" }},
		{"posting", func(p *LabelProof) { p.PostingNumber = "other" }},
		{"commercial order", func(p *LabelProof) { p.CommercialOrderID = "other" }},
		{"warehouse", func(p *LabelProof) { p.WarehouseID = "other" }},
		{"delivery method", func(p *LabelProof) { p.DeliveryMethodID = "other" }},
		{"work", func(p *LabelProof) { p.WorkID = "other" }},
		{"task", func(p *LabelProof) { p.TaskID = "" }},
		{"expired", func(p *LabelProof) { p.ExpiresAt = now }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			changed := proof
			tc.mutate(&changed)
			if !errors.Is(changed.ValidateFor(posting, "label-work", now), ErrStale) {
				t.Fatal("substituted label proof accepted")
			}
		})
	}
}

func TestHybridLabelRequirementDoesNotBecomeGenericShippingAuthorization(t *testing.T) {
	t.Parallel()
	posting := Posting{Delivery: DeliveryFacts{IntegrationType: Present("hybrid")}}
	if !HybridLabelRequired(posting, ActionPostingShip) || HybridLabelRequired(posting, ActionPostingLabelCreate) {
		t.Fatal("incorrect hybrid action classification")
	}
	posting.Delivery.IntegrationType = Present("non_integrated")
	if HybridLabelRequired(posting, ActionPostingShip) {
		t.Fatal("ordinary seller delivery requires hybrid-only label proof")
	}
}
