package delivery

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestMethodIntegrationSeparatesCarrierEvidenceFromAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, carrier string }{{"non_integrated", "non_integrated"}, {"aggregator", "aggregator"}, {"3pl_tracking", ""}, {"hybrid", ""}, {"future_integration", ""}} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			warehouse := Warehouse{ID: "warehouse", Type: orders.Present("rfbs"), Express: orders.Present(false)}
			method := Method{ID: "method", WarehouseID: warehouse.ID, IntegrationType: orders.Present(tc.raw)}
			profile, err := Classify(Scope{"tenant", "account"}, warehouse, &method, testEvidence(t), time.Now())
			if err != nil || known(profile.MethodIntegrationType) != tc.raw || known(profile.CarrierIntegration) != tc.carrier {
				t.Fatalf("observed integration mapping incorrect: %+v %v", profile, err)
			}
			if slices.Contains(profile.VerifiedDimensions, "carrier") != (tc.carrier != "") {
				t.Fatal("registration type invented carrier transition authority")
			}
			capability := profile.CapabilityRef()
			if capability == nil || known(capability.Carrier) != tc.carrier || capability.Destination.IsKnown() {
				t.Fatal("carrier capability lost evidence or invented destination")
			}
			if tc.raw == "future_integration" && !slices.ContainsFunc(profile.Drift, func(d orders.Drift) bool { return d.Code == "unknown_enum" }) {
				t.Fatal("new integration enum was silently accepted")
			}
		})
	}
}

func TestMissingMethodIntegrationPreservesLegacyProfileIdentity(t *testing.T) {
	t.Parallel()
	profile, err := Classify(Scope{"tenant", "account"}, Warehouse{ID: "warehouse"}, nil, testEvidence(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(profile)
	if err != nil || strings.Contains(string(raw), "method_integration_type") {
		t.Fatal("absent observation altered legacy identity")
	}
	var restored Profile
	if err = json.Unmarshal(raw, &restored); err != nil || restored.Validate() != nil || restored.ID != profile.ID {
		t.Fatal("legacy content hash no longer verifies")
	}
}

func TestMethodAndPostingIntegrationConflictRequiresExactTarget(t *testing.T) {
	t.Parallel()
	warehouse := Warehouse{ID: "warehouse", Type: orders.Present("rfbs")}
	method := Method{ID: "method", WarehouseID: warehouse.ID, IntegrationType: orders.Present("non_integrated")}
	profile, err := Classify(Scope{"tenant", "account"}, warehouse, &method, testEvidence(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	posting := orders.Posting{Scope: orders.Scope{Tenant: "tenant", OzonAccount: "account", MoyskladAccount: "moy"}, WarehouseID: orders.Present("warehouse"), DeliveryMethodID: orders.Present("method"), Delivery: orders.DeliveryFacts{IntegrationType: orders.Present("hybrid")}}
	if !profile.IntegrationConflicts(posting) {
		t.Fatal("contradictory fresh method/posting metadata ignored")
	}
	posting.Delivery.IntegrationType = orders.Present("non_integrated")
	if profile.IntegrationConflicts(posting) {
		t.Fatal("matching integration marked conflict")
	}
	posting.Delivery.IntegrationType = orders.Field[string]{}
	if profile.IntegrationConflicts(posting) {
		t.Fatal("missing is not a contradictory value")
	}
}
