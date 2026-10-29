// Package commitment evaluates evidence without I/O or external side effects.
package commitment

import (
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

const (
	ProofOwnership        = "OWNERSHIP_NOT_REGISTERED"
	ProofProductMapping   = "PRODUCT_MAPPING_NOT_CONFIRMED"
	ProofWarehouseMapping = "WAREHOUSE_MAPPING_NOT_CONFIRMED"
	ProofUnit             = "SALE_UNIT_UNVERIFIED"
	ProofBaseline         = "SOURCE_BASELINE_NOT_PROVEN"
	ProofSourceOrdering   = "SOURCE_ORDERING_UNPROVEN"
	ProofUnassigned       = "UNASSIGNED_RESERVE_COVERAGE_MISSING"
	ProofAttribution      = "RESERVATION_ATTRIBUTION_UNKNOWN"
	ProofPostingCoverage  = "POSTING_COVERAGE_INSUFFICIENT"
	ProofCompeting        = "COMPETING_EXPOSURE_NOT_VERIFIED"
	ProofBound            = "UNSEEN_REDUCTION_BOUND_UNPROVEN"
	ProofCustomerOrders   = "CUSTOMER_ORDER_COVERAGE_VERIFIED"
)

var proofDescriptions = map[string]string{
	ProofOwnership:        "Tenant/account ownership is registered by a trusted administrator.",
	ProofProductMapping:   "A confirmed, scoped product mapping exists.",
	ProofWarehouseMapping: "A confirmed, scoped warehouse mapping exists.",
	ProofUnit:             "A one-to-one sale unit and simple composition are established.",
	ProofBaseline:         "A consistent source baseline and visibility are established.",
	ProofSourceOrdering:   "Source ordering or an equivalent watermark is established.",
	ProofUnassigned:       "Warehouse-unassigned reserves are covered.",
	ProofAttribution:      "Overlap of source reservations and channel obligations is established.",
	ProofPostingCoverage:  "Channel obligations, including pre-posting reserves, are covered.",
	ProofCompeting:        "Exclusive control and competing channel exposure are established.",
	ProofBound:            "A disjoint bound for unseen reductions is established.",
}

// Evaluate returns the same semantic decision for the same input and clock.
// Only whole, nonnegative pieces are supported; no rounding is performed.
func Evaluate(req domain.StockCalculationRequest, e domain.StockEvidence, now time.Time) domain.StockDecision {
	now = now.UTC()
	sourceFreshness := SourceFreshnessTime(e.Source, now)
	d := domain.StockDecision{Request: req, Evidence: e, EvaluatedAt: now, Status: domain.StockReady, ListingMayBeExposed: true, ReasonCodes: []string{}}
	type check struct {
		code, message, ref string
		passed, blocked    bool
	}
	checks := make([]check, 0, len(proofDescriptions)+12)
	add := func(code, message, ref string, passed, blocked bool) {
		checks = append(checks, check{code, message, ref, passed, blocked})
	}
	validPolicy := !now.IsZero() && req.Policy.Version > 0 && req.Policy.MaxObservationAge > 0 && req.Policy.DecisionLifetime > 0
	add("INVALID_POLICY", "Policy version and durations must be positive.", "", validPolicy, true)
	validIdentity := strings.TrimSpace(req.Pool.Tenant) != "" && strings.TrimSpace(req.Pool.MoyskladAccount) != "" && strings.TrimSpace(req.Pool.WarehouseID) != "" && strings.TrimSpace(req.Pool.ItemID) != "" && (req.Pool.ItemKind == "product" || req.Pool.ItemKind == "variant") && strings.TrimSpace(req.Target.OzonAccount) != "" && strings.TrimSpace(req.Target.WarehouseID) != "" && strings.TrimSpace(req.Target.ProductID) != ""
	add("INVALID_POOL_OR_TARGET", "Pool and target have valid scoped identities.", "", validIdentity, true)
	add("UNIT_UNSUPPORTED", "The unit is a whole piece; no conversion or rounding is performed.", "", req.Policy.Unit == "piece", true)
	add("SHARED_POOL_UNSUPPORTED", "The pool does not have multiple mapped offers.", "", !e.SharedPool, true)
	productScope := e.ProductMapping == nil || (e.ProductMapping.Tenant == req.Pool.Tenant && e.ProductMapping.OzonAccount == req.Target.OzonAccount && e.ProductMapping.MoyskladAccount == req.Pool.MoyskladAccount && e.ProductMapping.OzonProductID == req.Target.ProductID && e.ProductMapping.ItemKind == req.Pool.ItemKind && e.ProductMapping.ItemID == req.Pool.ItemID)
	warehouseScope := e.WarehouseMapping == nil || (e.WarehouseMapping.Tenant == req.Pool.Tenant && e.WarehouseMapping.OzonAccount == req.Target.OzonAccount && e.WarehouseMapping.MoyskladAccount == req.Pool.MoyskladAccount && e.WarehouseMapping.MoyskladWarehouseID == req.Pool.WarehouseID && e.WarehouseMapping.OzonWarehouseID == req.Target.WarehouseID)
	add("PRODUCT_MAPPING_CONFLICT", "The product mapping does not conflict with this pool.", "", !e.ProductConflict && productScope, true)
	add("WAREHOUSE_MAPPING_CONFLICT", "The warehouse mapping does not conflict with this pool or another account pair.", "", !e.WarehouseConflict && warehouseScope, true)
	validProduct := e.ProductMapping != nil && productScope && e.ProductMapping.ID > 0 && e.ProductMapping.Version > 0 && strings.TrimSpace(e.ProductMapping.OfferID) != "" && strings.TrimSpace(e.ProductMapping.Provenance) != "" && !e.ProductMapping.ConfirmedAt.IsZero() && !e.ProductMapping.ConfirmedAt.After(now)
	validWarehouse := e.WarehouseMapping != nil && warehouseScope && e.WarehouseMapping.ID > 0 && e.WarehouseMapping.Version > 0 && strings.TrimSpace(e.WarehouseMapping.Provenance) != "" && !e.WarehouseMapping.ConfirmedAt.IsZero() && !e.WarehouseMapping.ConfirmedAt.After(now)
	add("PRODUCT_MAPPING_REFERENCE_MISSING", "A valid product mapping version is frozen.", "", validProduct, false)
	add("WAREHOUSE_MAPPING_REFERENCE_MISSING", "A valid warehouse mapping version is frozen.", "", validWarehouse, false)
	sourceScope := (!e.Source.ObservationFound && e.Source.ImportID == nil) || (e.Source.Tenant == req.Pool.Tenant && e.Source.Account == req.Pool.MoyskladAccount && e.Source.WarehouseID == req.Pool.WarehouseID && e.Source.ItemKind == req.Pool.ItemKind && e.Source.ItemID == req.Pool.ItemID)
	marketScope := (e.Marketplace.ImportID == nil && !e.Marketplace.StockRowFound && e.Marketplace.PostingBatchID == nil) || (e.Marketplace.Tenant == req.Pool.Tenant && e.Marketplace.Account == req.Target.OzonAccount && e.Marketplace.WarehouseID == req.Target.WarehouseID && e.Marketplace.ProductID == req.Target.ProductID)
	add("SOURCE_EVIDENCE_SCOPE_CONFLICT", "Source evidence belongs to this stock pool.", "", sourceScope, true)
	add("MARKETPLACE_EVIDENCE_SCOPE_CONFLICT", "Marketplace evidence belongs to this tenant and target.", "", marketScope, true)
	add("SOURCE_OBSERVATION_MISSING", "A warehouse stock observation exists in the selected import.", "", e.Source.ObservationFound && e.Source.ImportID != nil && *e.Source.ImportID > 0, false)
	add("SOURCE_TRAVERSAL_INCOMPLETE", "The selected import traversal completed.", "", e.Source.TraversalComplete, false)
	observationFresh := false
	if sourceFreshness != nil {
		age := now.Sub(*sourceFreshness)
		ageText := age.String()
		d.SourceObservationAge = &ageText
		observationFresh = validPolicy && age >= 0 && age < req.Policy.MaxObservationAge
	}
	add("SOURCE_OBSERVATION_STALE", "The local observation time is within the policy age.", "", observationFresh, false)
	var customerCoverageExpiry *time.Time
	if e.CustomerOrders != nil {
		orders := e.CustomerOrders
		coverage, provided := e.Proofs[ProofCustomerOrders]
		coverageLimit := 15 * time.Minute
		if coverage.State == domain.ProofAttested && req.Policy.AcceptOperatorAttested && req.Policy.TestStandingAttestationMaxAge > 0 && req.Policy.TestStandingAttestationMaxAge <= 24*time.Hour {
			coverageLimit = req.Policy.TestStandingAttestationMaxAge
		}
		coverageVerified := provided && proofAccepted(coverage, req.Policy) && strings.TrimSpace(coverage.Reference) != "" && !coverage.AsOf.IsZero() && !coverage.AsOf.After(now) && coverage.ValidUntil.After(now) && sourceFreshness != nil && !coverage.AsOf.After(*sourceFreshness) && coverage.ValidUntil.Sub(coverage.AsOf) <= coverageLimit
		coverageDescription := "Complete customer-order and reservation coverage, including baseline inclusion, was explicitly verified."
		if coverage.State == domain.ProofAttested {
			coverageDescription = "Customer-order coverage is an operator-attested test assumption, not independent provider evidence."
		}
		if provided {
			add(ProofCustomerOrders, coverageDescription, coverage.Reference, coverageVerified, false)
		}
		if coverageVerified {
			expiry := coverage.ValidUntil
			customerCoverageExpiry = &expiry
		}
		add("CUSTOMER_ORDER_IMPORT_MISSING", "A scoped customer-order traversal or accepted, explicitly classified coverage assumption was recorded.", coverage.Reference, orders.BatchID != nil || coverageVerified, false)
		add("CUSTOMER_ORDER_EVIDENCE_TRUNCATED", "The bounded customer-order evidence was not truncated.", "", !orders.Truncated, false)
		add("CUSTOMER_ORDER_DETAILS_WITHOUT_BATCH", "Observed order details have a durable scoped batch reference.", "", orders.BatchID != nil || len(orders.Correlations) == 0, true)
		if orders.BatchID != nil {
			add("CUSTOMER_ORDER_POSTING_EVIDENCE_MISSING", "A relevant scoped posting observation was considered.", "", len(orders.Correlations) > 0 || coverageVerified, false)
			add("CUSTOMER_ORDER_BASELINE_INCLUSION_UNKNOWN", "Position reserve inclusion in the stock baseline is proven.", coverage.Reference, coverageVerified, false)
			states := map[domain.OrderCorrelationStatus]bool{}
			reserveObserved := false
			reserveValid := true
			for _, c := range orders.Correlations {
				states[c.Status] = true
				if c.Truncated {
					states[domain.OrderInsufficient] = true
				}
				for _, candidate := range c.Candidates {
					for _, p := range candidate.Positions {
						if p.Reserve != nil {
							reserveObserved = true
							if _, ok := wholeNonnegative(*p.Reserve); !ok {
								reserveValid = false
							}
						}
					}
				}
			}
			for status := range states {
				add("CUSTOMER_ORDER_"+string(status), "The customer-order correlation has an independently verified identity.", "", status == domain.OrderIdentityConfirmed, false)
			}
			add("CUSTOMER_ORDER_RESERVE_MISSING", "A position reserve value was observed or independently verified empty coverage was supplied.", "", reserveObserved || (coverageVerified && len(orders.Correlations) == 0), false)
			add("CUSTOMER_ORDER_RESERVE_UNSUPPORTED", "Observed customer-order reserves are nonnegative whole pieces.", "", reserveValid, true)
		}
	}

	values := []struct {
		code, invalidCode, message string
		value                      *string
		blocked                    bool
	}{
		{"PHYSICAL_STOCK_MISSING", "PHYSICAL_STOCK_UNSUPPORTED", "Physical stock is present.", e.Source.Physical, false},
		{"SOURCE_RESERVE_MISSING", "SOURCE_RESERVE_UNSUPPORTED", "Source reserve is present.", e.Source.Reserve, false},
		{"UNREFLECTED_OBLIGATIONS_MISSING", "UNREFLECTED_OBLIGATIONS_UNSUPPORTED", "The unreflected obligation quantity is present.", e.Unreflected, false},
		{"UNSEEN_REDUCTION_BOUND_MISSING", "UNSEEN_REDUCTION_BOUND_UNSUPPORTED", "The unseen-reduction bound is present.", e.UnseenBound, false},
	}
	parsed := make([]*big.Int, 0, 5)
	for _, v := range values {
		if v.value == nil {
			add(v.code, v.message, "", false, v.blocked)
			parsed = append(parsed, nil)
			continue
		}
		n, ok := wholeNonnegative(*v.value)
		add(v.code, v.message, "", true, false)
		add(v.invalidCode, "The quantity is nonnegative, whole, well formed, and within the size limit.", "", ok, true)
		parsed = append(parsed, n)
	}
	safety, safetyOK := wholeNonnegative(req.Policy.SafetyStock)
	add("INVALID_SAFETY_STOCK", "Safety stock must be a nonnegative whole number of pieces.", "", safetyOK, true)
	parsed = append(parsed, safety)

	expires := now.Add(req.Policy.DecisionLifetime)
	if customerCoverageExpiry != nil && customerCoverageExpiry.Before(expires) {
		expires = *customerCoverageExpiry
	}
	if sourceFreshness != nil && validPolicy {
		if ageExpiry := sourceFreshness.Add(req.Policy.MaxObservationAge); ageExpiry.Before(expires) {
			expires = ageExpiry
		}
	}
	for code, description := range proofDescriptions {
		p, ok := e.Proofs[code]
		verified := ok && proofAccepted(p, req.Policy) && strings.TrimSpace(p.Reference) != "" && !p.AsOf.IsZero() && !p.AsOf.After(now) && p.ValidUntil.After(now)
		if p.State == domain.ProofAttested {
			description = "Operator-attested test assumption, not independently verified: " + description
		}
		if verified && code != ProofOwnership && code != ProofProductMapping && code != ProofWarehouseMapping && code != ProofUnit {
			verified = sourceFreshness != nil && !p.AsOf.After(*sourceFreshness)
		}
		if verified && p.ValidUntil.Before(expires) {
			expires = p.ValidUntil
		}
		blocked := p.State == domain.ProofUnsupported || (p.State == domain.ProofConflict && (code == ProofOwnership || code == ProofProductMapping || code == ProofWarehouseMapping || code == ProofUnit))
		add(code, description, p.Reference, verified, blocked)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].code < checks[j].code })
	for _, c := range checks {
		message := c.message
		if !c.passed {
			message = "Not satisfied: " + message
		}
		d.Checks = append(d.Checks, domain.StockCheck{Code: c.code, Passed: c.passed, Message: message, Evidence: c.ref})
		if !c.passed {
			d.ReasonCodes = append(d.ReasonCodes, c.code)
			if c.blocked {
				d.Status = domain.StockBlocked
			} else if d.Status == domain.StockReady {
				d.Status = domain.StockUnknown
			}
		}
	}
	if d.Status == domain.StockReady {
		result := new(big.Int).Set(parsed[0])
		for _, n := range parsed[1:] {
			result.Sub(result, n)
		}
		if result.Sign() < 0 {
			result.SetInt64(0)
		}
		quantity := result.String()
		d.Quantity = &quantity
		d.ExpiresAt = &expires
	}
	return d
}

func proofAccepted(proof domain.StockProof, policy domain.StockCalculationPolicy) bool {
	return proof.State == domain.ProofVerified || (policy.AcceptOperatorAttested && proof.State == domain.ProofAttested && proof.EvidenceClass == "OPERATOR_ATTESTED")
}

// SourceFreshnessTime keeps the original row timestamp intact while allowing
// a completed, same-content durable traversal to serve as a conservative
// freshness boundary. It does not assert a source-side timestamp or snapshot.
func SourceFreshnessTime(source domain.StockSourceEvidence, now time.Time) *time.Time {
	if t := source.FreshTraversal; t != nil && source.ImportID != nil && t.ImportID == *source.ImportID && t.OperationID != "" && !t.StartedAt.IsZero() && !t.FinishedAt.Before(t.StartedAt) && !t.FinishedAt.After(now) {
		return &t.StartedAt
	}
	return source.ObservedAt
}

func wholeNonnegative(s string) (*big.Int, bool) {
	if len(s) == 0 || len(s) > 80 {
		return nil, false
	}
	dots := 0
	for i, r := range s {
		if r == '.' {
			dots++
			if dots > 1 || i == 0 || i == len(s)-1 {
				return nil, false
			}
			continue
		}
		if r < '0' || r > '9' {
			return nil, false
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 || !r.IsInt() {
		return nil, false
	}
	return new(big.Int).Set(r.Num()), true
}
