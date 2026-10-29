package catalogsync

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestBlockedDecisionPreservesEnrollmentIdentitiesWithoutObservedFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		code   string
		change func(*TargetContext)
	}{
		{"unresolved outcome", "TARGET_OUTCOME_UNRESOLVED", func(target *TargetContext) { target.Fenced = true }},
		{"creation needs reviewed mapping", "CREATED_TARGET_REQUIRES_MAPPING", func(target *TargetContext) {
			target.Enrollment.Create = &Creation{}
			created := target.Enrollment.Target
			created.ID = "actual-created-id"
			target.CreatedTarget = &created
		}},
		{"mapping changed", "CONFIRMED_MAPPING_CHANGED", func(*TargetContext) {}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := identityOnlyTarget()
			tc.change(&target)
			decision := (Service{}).decide(context.Background(), ReviewContext{}, target, nil)
			if decision.Ready || len(decision.Commands) != 0 || len(decision.Validation) != 1 || decision.Validation[0].Code != tc.code {
				t.Fatalf("unexpected blocked decision: %+v", decision)
			}
			assertUnobservedIdentity(t, decision.Source, target.Enrollment.Source)
			assertUnobservedIdentity(t, decision.Target, target.Enrollment.Target)
		})
	}
}

func identityOnlyTarget() TargetContext {
	return TargetContext{Enrollment: Enrollment{ID: "enrollment", Source: catalog.Identity{
		Account: catalog.Account{Provider: catalog.ProviderOzon, ID: "ozon-account"}, EntityType: "product", ID: "42", OfferID: "SOURCE-OFFER"},
		Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: "moy-account"}, EntityType: "product"}}}
}

func assertUnobservedIdentity(t *testing.T, product catalog.Product, identity catalog.Identity) {
	t.Helper()
	if product.Identity != identity {
		t.Fatalf("blocked decision lost the exact enrollment identity: %+v", product.Identity)
	}
	if len(product.Fields) != 0 || product.Revision != "" || !product.ObservedAt.IsZero() || product.Lifecycle != "" || product.Parent != nil || product.Variant {
		t.Fatalf("blocked decision fabricated provider observations: %+v", product)
	}
}
