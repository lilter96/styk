package delivery

import (
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

func testEvidence(t *testing.T) ContractEvidence {
	t.Helper()
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := Evidence(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestClassifyIndependentFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, kind, status, scheme string
		rfbs, express              orders.Field[bool]
	}{
		{"created_fbs", "fbs", "created", "FBS", orders.Present(false), orders.Present(false)},
		{"existing_active_fbs", "fbs", "active", "FBS", orders.Present(false), orders.Present(false)},
		{"disabled_rfbs", "rfbs", "disabled", "rFBS", orders.Present(true), orders.Present(false)},
		{"express", "rfbs_express", "created", "rFBS", orders.Present(true), orders.Present(true)},
		{"express_unknown", "rfbs_express", "created", "rFBS", orders.Present(true), orders.Field[bool]{}},
		{"unknown_type", "future_type", "created", "", orders.Present(false), orders.Field[bool]{}},
		{"conflict", "fbs", "created", "", orders.Present(true), orders.Present(false)},
		{"fbo", "fbo", "created", "FBO", orders.Present(false), orders.Field[bool]{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := Warehouse{ID: "9007199254740993", Type: orders.Present(tc.kind), Status: orders.Present(tc.status), IsRFBS: tc.rfbs, Express: tc.express}
			p, err := Classify(Scope{"tenant", "account"}, w, nil, testEvidence(t), time.Now().UTC())
			if err != nil || known(p.Scheme) != tc.scheme || known(p.WarehouseStatus) != tc.status || p.Express.IsKnown() != tc.express.IsKnown() {
				t.Fatalf("classification: %+v, %v", p, err)
			}
			if p.CarrierIntegration.IsKnown() || p.DestinationType.IsKnown() || p.CapabilityRef() != nil || p.MethodVerified {
				t.Fatal("unobserved delivery or method dimension was fabricated")
			}
		})
	}
}

func known[T any](field orders.Field[T]) T {
	if field.IsKnown() {
		return *field.Value
	}
	var empty T
	return empty
}

func TestMethodNamespacesAndStableRevision(t *testing.T) {
	t.Parallel()
	w := Warehouse{ID: "9007199254740993", Type: orders.Present("rfbs"), Status: orders.Present("created")}
	method := Method{ID: w.ID, WarehouseID: w.ID, Express: orders.Present(true)}
	checked := time.Now().UTC()
	p, err := Classify(Scope{"tenant", "account"}, w, &method, testEvidence(t), checked)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := Classify(p.Scope, w, &method, p.Contract, checked.Add(time.Minute))
	if err != nil || p.ID != refreshed.ID || p.Revision != refreshed.Revision || p.CheckedAt.Equal(refreshed.CheckedAt) {
		t.Fatal("fresh timestamps changed fact identity")
	}
	ref := p.CapabilityRef()
	if ref == nil || ref.WarehouseID != w.ID || ref.DeliveryMethodID != method.ID || !slices.Contains(p.VerifiedDimensions, "delivery_method") || !ref.Express.IsKnown() || ref.Carrier.IsKnown() || ref.Destination.IsKnown() {
		t.Fatal("method identity or partial capability dimensions lost")
	}
	method.WarehouseID = "different"
	mismatch, err := Classify(p.Scope, w, &method, p.Contract, checked)
	if err != nil || mismatch.MethodVerified || mismatch.MethodID.IsKnown() {
		t.Fatal("equal numeric IDs substituted for explicit warehouse link")
	}
}

func TestExactContractAndTimeValidity(t *testing.T) {
	t.Parallel()
	checked := time.Now().UTC()
	p, err := Classify(Scope{"tenant", "account"}, Warehouse{ID: "1", Type: orders.Present("fbs")}, nil, testEvidence(t), checked)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Fresh(checked, p.Contract) || p.Fresh(checked.Add(Validity), p.Contract) || p.Fresh(checked.Add(-time.Second), p.Contract) {
		t.Fatal("observation validity is not bounded")
	}
	changed := p.Contract
	changed.Hash = "another_contract"
	if p.Fresh(checked, changed) || p.Contract.SchemaVerified || !p.Contract.VersionsVerified {
		t.Fatal("endpoint version or outdated hash became a verified schema")
	}
}

func TestCapabilityMatrixEvidenceStatesAreIndependent(t *testing.T) {
	t.Parallel()
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	capability := manifest.Capabilities[0]
	coverage := map[string]Coverage{CapabilityKey(capability): {Implemented: true, Deterministic: true}}
	matrix := CapabilityMatrix(manifest, coverage)
	if len(matrix) != len(manifest.Capabilities) || !matrix[0].Implemented || !matrix[0].Deterministic || matrix[0].ObservedLive || matrix[0].ContractVerified || len(matrix[0].Prerequisites) == 0 {
		t.Fatal("implementation/test evidence incorrectly implied contract or live verification")
	}
}
