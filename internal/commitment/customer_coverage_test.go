package commitment

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func TestExplicitCustomerCoverageIsBoundedAndNotImplicit(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*domain.StockProof)
		ready  bool
	}{{"explicit current coverage", func(*domain.StockProof) {}, true}, {"expired", func(p *domain.StockProof) { p.ValidUntil = p.AsOf }, false}, {"after baseline", func(p *domain.StockProof) { p.AsOf = p.AsOf.Add(2 * time.Minute) }, false}, {"unbounded", func(p *domain.StockProof) { p.ValidUntil = p.AsOf.Add(time.Hour) }, false}, {"no provenance", func(p *domain.StockProof) { p.Reference = "" }, false}} {
		t.Run(test.name, func(t *testing.T) {
			r, e, now := provenFixture()
			e.CustomerOrders = &domain.StockCustomerOrderEvidence{}
			if Evaluate(r, e, now).Status == domain.StockReady {
				t.Fatal("missing order coverage was implicitly accepted")
			}
			proof := domain.StockProof{State: domain.ProofVerified, Reference: "operator-attested: independently reconciled order/reservation register", AsOf: *e.Source.ObservedAt, ValidUntil: now.Add(5 * time.Minute)}
			test.change(&proof)
			e.Proofs[ProofCustomerOrders] = proof
			d := Evaluate(r, e, now)
			if (d.Status == domain.StockReady) != test.ready {
				t.Fatalf("coverage state: %s %v", d.Status, d.ReasonCodes)
			}
		})
	}
}
func TestCustomerCoverageNeverOverridesKnownContradictions(t *testing.T) {
	for _, name := range []string{"ambiguous identity", "conflicting identity", "truncated", "negative reserve", "wrong account", "details without batch"} {
		t.Run(name, func(t *testing.T) {
			r, e, now := provenFixture()
			batch := int64(7)
			reserve := "1"
			e.CustomerOrders = &domain.StockCustomerOrderEvidence{BatchID: &batch, Correlations: []domain.OrderCorrelation{{Status: domain.OrderIdentityConfirmed, Candidates: []domain.OrderCorrelationCandidate{{Positions: []domain.OrderPositionEvidence{{Reserve: &reserve}}}}}}}
			e.Proofs[ProofCustomerOrders] = domain.StockProof{State: domain.ProofVerified, Reference: "operator-attested: verified scoped complete order controls", AsOf: *e.Source.ObservedAt, ValidUntil: now.Add(5 * time.Minute)}
			switch name {
			case "ambiguous identity":
				e.CustomerOrders.Correlations[0].Status = domain.OrderAmbiguous
			case "conflicting identity":
				e.CustomerOrders.Correlations[0].Status = domain.OrderConflict
			case "truncated":
				e.CustomerOrders.Truncated = true
			case "negative reserve":
				negative := "-1"
				e.CustomerOrders.Correlations[0].Candidates[0].Positions[0].Reserve = &negative
			case "wrong account":
				e.Source.Account = "another-account"
			case "details without batch":
				e.CustomerOrders.BatchID = nil
			}
			d := Evaluate(r, e, now)
			if d.Status == domain.StockReady || d.Quantity != nil {
				t.Fatalf("known contradiction overridden: %s", name)
			}
		})
	}
}

func TestStandingTestAssumptionIsExplicitBoundedAndNotFreshEvidence(t *testing.T) {
	r, e, now := provenFixture()
	e.CustomerOrders = &domain.StockCustomerOrderEvidence{}
	original := now.Add(-time.Hour)
	e.Proofs[ProofCustomerOrders] = domain.StockProof{State: domain.ProofAttested, EvidenceClass: "OPERATOR_ATTESTED", Reference: "original test policy", AsOf: original, ValidUntil: original.Add(2 * time.Hour)}
	r.Policy.AcceptOperatorAttested = true
	if d := Evaluate(r, e, now); d.Status == domain.StockReady {
		t.Fatal("ordinary short-lived policy accepted a standing assumption")
	}
	r.Policy.TestStandingAttestationMaxAge = 24 * time.Hour
	d := Evaluate(r, e, now)
	if d.Status != domain.StockReady || !d.Evidence.Proofs[ProofCustomerOrders].AsOf.Equal(original) {
		t.Fatalf("standing assumption was lost or refreshed: %+v", d.ReasonCodes)
	}
	proof := e.Proofs[ProofCustomerOrders]
	proof.ValidUntil = original.Add(25 * time.Hour)
	e.Proofs[ProofCustomerOrders] = proof
	if d = Evaluate(r, e, now); d.Status == domain.StockReady {
		t.Fatal("policy exceeded its maximum validity")
	}
	proof.ValidUntil = original.Add(2 * time.Hour)
	proof.State = domain.ProofVerified
	e.Proofs[ProofCustomerOrders] = proof
	if d = Evaluate(r, e, now); d.Status == domain.StockReady {
		t.Fatal("long validity was applied to purported provider evidence")
	}
}
