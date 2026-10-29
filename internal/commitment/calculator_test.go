package commitment

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func textPtr(s string) *string { return &s }

func provenFixture() (domain.StockCalculationRequest, domain.StockEvidence, time.Time) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	observed := now.Add(-time.Minute)
	id := int64(1)
	r := domain.StockCalculationRequest{
		Pool:   domain.StockPool{Tenant: "tenant", MoyskladAccount: "source", WarehouseID: "warehouse", ItemKind: "variant", ItemID: "item"},
		Target: domain.StockTarget{OzonAccount: "seller", WarehouseID: "destination", ProductID: "product"},
		Policy: domain.StockCalculationPolicy{Version: 1, Unit: "piece", SafetyStock: "1", MaxObservationAge: time.Hour, DecisionLifetime: 5 * time.Minute},
	}
	e := domain.StockEvidence{
		Source:           domain.StockSourceEvidence{Tenant: "tenant", Account: "source", WarehouseID: "warehouse", ItemKind: "variant", ItemID: "item", ImportID: &id, ObservationFound: true, Physical: textPtr("10"), Reserve: textPtr("1"), ObservedAt: &observed, TimeBasis: "synthetic", TraversalComplete: true},
		ProductMapping:   &domain.MappingReference{ID: 1, Version: 1, Tenant: "tenant", OzonAccount: "seller", MoyskladAccount: "source", OzonProductID: "product", OfferID: "offer", ItemKind: "variant", ItemID: "item", Provenance: "synthetic", ConfirmedAt: observed},
		WarehouseMapping: &domain.MappingReference{ID: 2, Version: 1, Tenant: "tenant", OzonAccount: "seller", MoyskladAccount: "source", MoyskladWarehouseID: "warehouse", OzonWarehouseID: "destination", Provenance: "synthetic", ConfirmedAt: observed},
		Unreflected:      textPtr("2"), UnseenBound: textPtr("0"), Proofs: make(map[string]domain.StockProof),
	}
	for code := range proofDescriptions {
		e.Proofs[code] = domain.StockProof{State: domain.ProofVerified, Reference: "synthetic:" + code, AsOf: observed, ValidUntil: now.Add(10 * time.Minute)}
	}
	return r, e, now
}

func hasReason(d domain.StockDecision, reason string) bool {
	i := sort.SearchStrings(d.ReasonCodes, reason)
	return i < len(d.ReasonCodes) && d.ReasonCodes[i] == reason
}

func TestConditionalCalculationAndReflection(t *testing.T) {
	for _, tc := range []struct{ name, physical, reserve, unreflected, safety, bound, want string }{
		{"unreflected", "10", "1", "2", "1", "0", "6"},
		{"fully reflected", "10", "3", "0", "1", "0", "6"},
		{"reservation subtracted once", "10", "3", "0", "0", "0", "7"},
		{"partially reflected", "10", "2", "1", "1", "0", "6"},
		{"zero reported stock", "0", "0", "0", "0", "0", "0"},
		{"safety exceeds stock", "2", "0", "0", "5", "0", "0"},
		{"large exact numbers", "1000000000000000000000001", "1", "0", "0", "0", "1000000000000000000000000"},
		{"whole decimal input", "10.00", "1.0", "2.0", "1", "0", "6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e, now := provenFixture()
			e.Source.Physical, e.Source.Reserve, e.Unreflected, e.UnseenBound = textPtr(tc.physical), textPtr(tc.reserve), textPtr(tc.unreflected), textPtr(tc.bound)
			r.Policy.SafetyStock = tc.safety
			d := Evaluate(r, e, now)
			if d.Status != domain.StockReady || d.Quantity == nil || *d.Quantity != tc.want || d.ExpiresAt == nil || !d.ExpiresAt.Equal(now.Add(5*time.Minute)) {
				t.Fatalf("decision: %+v", d)
			}
			if !d.ListingMayBeExposed || len(d.ReasonCodes) != 0 {
				t.Fatal("ready decision lost warning or acquired failures")
			}
			if d.SourceObservationAge == nil || *d.SourceObservationAge != "1m0s" {
				t.Fatal("local observation age not preserved")
			}
		})
	}
}

func TestFreshTraversalDoesNotRewriteHistoricalObservation(t *testing.T) {
	r, e, now := provenFixture()
	old := now.Add(-2 * time.Hour)
	e.Source.ObservedAt = &old
	started := now.Add(-2 * time.Minute)
	finished := now.Add(-time.Minute)
	for code, proof := range e.Proofs {
		proof.AsOf = started.Add(-time.Second)
		e.Proofs[code] = proof
	}
	stale := Evaluate(r, e, now)
	if !hasReason(stale, "SOURCE_OBSERVATION_STALE") {
		t.Fatal("an old observation without a new traversal must be stale")
	}
	e.Source.FreshTraversal = &domain.StockTraversalEvidence{OperationID: "scoped-completed-operation", ImportID: *e.Source.ImportID, StartedAt: started, FinishedAt: finished}
	fresh := Evaluate(r, e, now)
	if fresh.Status != domain.StockReady || fresh.Quantity == nil || *fresh.Quantity != "6" {
		t.Fatalf("matching fresh traversal did not restore eligibility: %+v", fresh.ReasonCodes)
	}
	if !fresh.Evidence.Source.ObservedAt.Equal(old) || fresh.SourceObservationAge == nil || *fresh.SourceObservationAge != "2m0s" {
		t.Fatal("the original observation time was overwritten or the start boundary was not used")
	}
	e.Source.FreshTraversal.ImportID++
	if got := Evaluate(r, e, now); !hasReason(got, "SOURCE_OBSERVATION_STALE") {
		t.Fatal("a traversal of another content batch cannot refresh this observation")
	}
	e.Source.FreshTraversal.ImportID--
	e.Source.FreshTraversal.FinishedAt = now.Add(time.Second)
	if got := Evaluate(r, e, now); !hasReason(got, "SOURCE_OBSERVATION_STALE") {
		t.Fatal("an unfinished traversal cannot refresh this observation")
	}
}

func TestOperatorAttestationsRequireExplicitTestPolicy(t *testing.T) {
	r, e, now := provenFixture()
	for _, code := range []string{ProofUnit, ProofBaseline, ProofSourceOrdering, ProofUnassigned, ProofAttribution, ProofPostingCoverage, ProofCompeting, ProofBound} {
		proof := e.Proofs[code]
		proof.State, proof.EvidenceClass = domain.ProofAttested, "OPERATOR_ATTESTED"
		e.Proofs[code] = proof
	}
	e.CustomerOrders = &domain.StockCustomerOrderEvidence{}
	e.Proofs[ProofCustomerOrders] = domain.StockProof{State: domain.ProofAttested, EvidenceClass: "OPERATOR_ATTESTED", Reference: "operator risk assumption", AsOf: *e.Source.ObservedAt, ValidUntil: now.Add(5 * time.Minute)}
	if d := Evaluate(r, e, now); d.Status == domain.StockReady {
		t.Fatal("ordinary policy accepted test-only assumptions")
	}
	r.Policy.AcceptOperatorAttested = true
	if d := Evaluate(r, e, now); d.Status != domain.StockReady || d.Quantity == nil || *d.Quantity != "6" || d.Evidence.Proofs[ProofCustomerOrders].EvidenceClass != "OPERATOR_ATTESTED" {
		t.Fatalf("test assumptions did not remain explicit: %+v", d.ReasonCodes)
	}
	bad := e.Proofs[ProofBound]
	bad.EvidenceClass = ""
	e.Proofs[ProofBound] = bad
	if d := Evaluate(r, e, now); d.Status == domain.StockReady {
		t.Fatal("unclassified attestation was accepted")
	}
}

func TestMissingAndConflictingEvidence(t *testing.T) {
	cases := []struct {
		name, reason string
		status       domain.StockDecisionStatus
		change       func(*domain.StockCalculationRequest, *domain.StockEvidence, time.Time)
	}{
		{"missing observation", "SOURCE_OBSERVATION_MISSING", domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.Source.ObservationFound = false
		}},
		{"missing stock", "PHYSICAL_STOCK_MISSING", domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) { e.Source.Physical = nil }},
		{"missing reserve", "SOURCE_RESERVE_MISSING", domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) { e.Source.Reserve = nil }},
		{"incomplete traversal", "SOURCE_TRAVERSAL_INCOMPLETE", domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.Source.TraversalComplete = false
		}},
		{"stale deduplicated import", "SOURCE_OBSERVATION_STALE", domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, n time.Time) {
			old := n.Add(-2 * time.Hour)
			e.Source.ObservedAt = &old
		}},
		{"missing source timestamp", ProofSourceOrdering, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			delete(e.Proofs, ProofSourceOrdering)
		}},
		{"unassigned reserves", ProofUnassigned, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			delete(e.Proofs, ProofUnassigned)
		}},
		{"baseline unproven", ProofBaseline, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			delete(e.Proofs, ProofBaseline)
		}},
		{"posting coverage", ProofPostingCoverage, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			delete(e.Proofs, ProofPostingCoverage)
		}},
		{"overlap unknown", ProofAttribution, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			delete(e.Proofs, ProofAttribution)
		}},
		{"conflicting partial reflection", ProofAttribution, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, n time.Time) {
			e.Proofs[ProofAttribution] = domain.StockProof{State: domain.ProofConflict, Reference: "contradiction", AsOf: n, ValidUntil: n.Add(time.Minute)}
		}},
		{"unconfirmed product", "PRODUCT_MAPPING_REFERENCE_MISSING", domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) { e.ProductMapping = nil }},
		{"cross account source", "SOURCE_EVIDENCE_SCOPE_CONFLICT", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.Source.Account = "other-source"
		}},
		{"cross tenant mapping", "PRODUCT_MAPPING_CONFLICT", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.ProductMapping.Tenant = "other-tenant"
		}},
		{"proof starts after baseline", ProofBound, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, n time.Time) {
			e.Proofs[ProofBound] = domain.StockProof{State: domain.ProofVerified, Reference: "late", AsOf: n, ValidUntil: n.Add(time.Minute)}
		}},
		{"product variant conflict", "PRODUCT_MAPPING_CONFLICT", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.ProductConflict = true
		}},
		{"warehouse conflict", "WAREHOUSE_MAPPING_CONFLICT", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.WarehouseConflict = true
		}},
		{"multiple offers", "SHARED_POOL_UNSUPPORTED", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) { e.SharedPool = true }},
		{"unsupported conversion", "UNIT_UNSUPPORTED", domain.StockBlocked, func(r *domain.StockCalculationRequest, _ *domain.StockEvidence, _ time.Time) {
			r.Policy.Unit = "kilogram"
		}},
		{"bundle or multipack", ProofUnit, domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, n time.Time) {
			e.Proofs[ProofUnit] = domain.StockProof{State: domain.ProofUnsupported, Reference: "pack", AsOf: n, ValidUntil: n.Add(time.Minute)}
		}},
		{"fractional stock", "PHYSICAL_STOCK_UNSUPPORTED", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.Source.Physical = textPtr("1.5")
		}},
		{"negative reserve", "SOURCE_RESERVE_UNSUPPORTED", domain.StockBlocked, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, _ time.Time) {
			e.Source.Reserve = textPtr("-1")
		}},
		{"negative safety", "INVALID_SAFETY_STOCK", domain.StockBlocked, func(r *domain.StockCalculationRequest, _ *domain.StockEvidence, _ time.Time) {
			r.Policy.SafetyStock = "-1"
		}},
		{"proof without provenance", ProofBaseline, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, n time.Time) {
			e.Proofs[ProofBaseline] = domain.StockProof{State: domain.ProofVerified, AsOf: n, ValidUntil: n.Add(time.Minute)}
		}},
		{"expired proof", ProofBound, domain.StockUnknown, func(_ *domain.StockCalculationRequest, e *domain.StockEvidence, n time.Time) {
			e.Proofs[ProofBound] = domain.StockProof{State: domain.ProofVerified, Reference: "old", AsOf: n.Add(-time.Hour), ValidUntil: n}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, e, now := provenFixture()
			tc.change(&r, &e, now)
			d := Evaluate(r, e, now)
			if d.Status != tc.status || d.Quantity != nil || d.ExpiresAt != nil || !hasReason(d, tc.reason) {
				t.Fatalf("want %s, %s, nil quantity: %+v", tc.status, tc.reason, d)
			}
			if !d.ListingMayBeExposed || !sort.StringsAreSorted(d.ReasonCodes) {
				t.Fatal("warning or deterministic reasons missing")
			}
		})
	}
}

func TestDeterministicReplayAndProofOrdering(t *testing.T) {
	r, e, now := provenFixture()
	delete(e.Proofs, ProofAttribution)
	delete(e.Proofs, ProofUnassigned)
	first := Evaluate(r, e, now)
	for i := 0; i < 20; i++ {
		if got := Evaluate(r, e, now); !reflect.DeepEqual(first, got) {
			t.Fatalf("different semantic result: %+v", got)
		}
	}
	if !hasReason(first, ProofAttribution) || !hasReason(first, ProofUnassigned) || len(first.Checks) <= len(first.ReasonCodes) {
		t.Fatal("missing failures or passed checks")
	}
	for i := 1; i < len(first.Checks); i++ {
		if first.Checks[i-1].Code >= first.Checks[i].Code {
			t.Fatal("checks not strictly sorted")
		}
	}
}

func TestConditionalCapacityMonotonic(t *testing.T) {
	for physical := 0; physical < 30; physical++ {
		for reserve := 0; reserve < 8; reserve++ {
			r, e, now := provenFixture()
			e.Source.Physical, e.Source.Reserve = textPtr(fmt.Sprint(physical)), textPtr(fmt.Sprint(reserve))
			before := Evaluate(r, e, now)
			e.Source.Reserve = textPtr(fmt.Sprint(reserve + 1))
			after := Evaluate(r, e, now)
			var a, b int
			if before.Quantity == nil || after.Quantity == nil {
				t.Fatal("synthetic proof unexpectedly blocked")
			}
			if _, err := fmt.Sscan(*before.Quantity, &a); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Sscan(*after.Quantity, &b); err != nil {
				t.Fatal(err)
			}
			if b > a {
				t.Fatalf("reserve increased capacity: %d > %d", b, a)
			}
		}
	}
}

func TestUnprovenCommitmentsRemainUnknown(t *testing.T) {
	r, e, now := provenFixture()
	// V1 has no ledger establishing this relationship; it cannot derive U.
	delete(e.Proofs, ProofAttribution)
	e.Unreflected = nil
	d := Evaluate(r, e, now)
	if d.Status != domain.StockUnknown || d.Quantity != nil || !hasReason(d, ProofAttribution) || !hasReason(d, "UNREFLECTED_OBLIGATIONS_MISSING") {
		t.Fatalf("unsafe result: %+v", d)
	}
}

func TestConfirmedOrderIdentityDoesNotProveStockBaseline(t *testing.T) {
	r, e, now := provenFixture()
	batch := int64(1)
	reserve := "2"
	e.CustomerOrders = &domain.StockCustomerOrderEvidence{BatchID: &batch, Correlations: []domain.OrderCorrelation{{Status: domain.OrderIdentityConfirmed, Candidates: []domain.OrderCorrelationCandidate{{Positions: []domain.OrderPositionEvidence{{Reserve: &reserve}}}}}}}
	d := Evaluate(r, e, now)
	if d.Status != domain.StockUnknown || d.Quantity != nil || !hasReason(d, "CUSTOMER_ORDER_BASELINE_INCLUSION_UNKNOWN") {
		t.Fatalf("identity link was promoted to baseline proof: %+v", d)
	}
}
