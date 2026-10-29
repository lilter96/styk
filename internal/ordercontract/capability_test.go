package ordercontract

import "testing"

func TestCapabilitiesRequireEveryExplicitDimension(t *testing.T) {
	t.Parallel()
	facts := CapabilityFacts{Scheme: "rFBS", WarehouseID: "TEST-WAREHOUSE", Carrier: "non_integrated",
		Destination: "customer", VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express", "destination"}}
	for i, dimension := range facts.VerifiedDimensions {
		t.Run(dimension, func(t *testing.T) {
			t.Parallel()
			incomplete := facts
			incomplete.VerifiedDimensions = append([]string{}, facts.VerifiedDimensions[:i]...)
			incomplete.VerifiedDimensions = append(incomplete.VerifiedDimensions, facts.VerifiedDimensions[i+1:]...)
			decision := AssessCapability(incomplete, "pack")
			if decision.Status != "blocked" || decision.Reason != "unverified_dimensions" {
				t.Fatalf("incomplete facts accepted: %+v", decision)
			}
		})
	}
}

func TestEveryWorkflowRespectsContractEvidence(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	for _, capability := range manifest.Capabilities {
		facts := CapabilityFacts{Scheme: capability.Scheme, WarehouseID: "TEST-WAREHOUSE", Carrier: capability.Carrier,
			Express: capability.Express, Destination: capability.Destination,
			VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express", "destination"}}
		decision := manifest.AssessCapability(facts, capability.Action)
		expected := "blocked"
		if capability.Status == "verified" {
			expected = "available"
		}
		if decision.Status != expected || decision.Endpoint != capability.Endpoint || len(decision.Prerequisites) == 0 {
			t.Fatalf("workflow incorrectly available: %+v", decision)
		}
	}
}

func TestSchemeIsNeverInferredFromEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct{ scheme, status, reason string }{
		{scheme: "FBO", status: "excluded", reason: "fbo_out_of_scope"},
		{scheme: "/v4/posting/fbs/list", status: "unsupported", reason: "no_verified_workflow"},
		{scheme: "unknown", status: "unsupported", reason: "no_verified_workflow"},
	}
	for _, tc := range tests {
		t.Run(tc.scheme, func(t *testing.T) {
			t.Parallel()
			facts := CapabilityFacts{Scheme: tc.scheme, WarehouseID: "TEST-WAREHOUSE", Carrier: "ozon", Destination: "customer",
				VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express", "destination"}}
			decision := AssessCapability(facts, "pack")
			if decision.Status != tc.status || decision.Reason != tc.reason {
				t.Fatalf("scheme inferred incorrectly: %+v", decision)
			}
		})
	}
}
