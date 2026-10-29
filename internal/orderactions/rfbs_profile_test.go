package orderactions

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestRFBSRouteUsesActualIntegrationAndDistinctProviderFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		posting func() orders.Posting
		route   RFBSRoute
		code    string
	}{
		{"own delivery", rfbsTestPosting, RFBSRouteSeller, ""},
		{"seller registered carrier", func() orders.Posting {
			p := rfbsTestPosting()
			p.Delivery.IntegrationType = orders.Present("3pl_tracking")
			return p
		}, RFBSRouteSellerTracking, "rfbs_tracking_workflow_unverified"},
		{"hybrid own null express", func() orders.Posting { return rfbsPVZPosting(false) }, RFBSRouteSellerPVZ, ""},
		{"hybrid partner and separate null provider", func() orders.Posting { return rfbsPVZPosting(true) }, RFBSRoutePartnerPVZ, ""},
		{"null tpl is not null provider", func() orders.Posting {
			p := rfbsPVZPosting(false)
			p.Delivery.ProviderID = orders.Field[string]{}
			p.Delivery.TPLProviderID = orders.Field[string]{Presence: orders.PresenceNull}
			return p
		}, RFBSRouteUnknown, "rfbs_hybrid_provider_evidence_required"},
		{"zero provider is not null", func() orders.Posting {
			p := rfbsPVZPosting(false)
			p.Delivery.ProviderID = orders.Present("0")
			return p
		}, RFBSRouteUnknown, "rfbs_hybrid_provider_evidence_required"},
		{"hybrid wrong destination", func() orders.Posting {
			p := rfbsPVZPosting(false)
			p.DeliveryProfile.Destination = orders.Present("customer")
			return p
		}, RFBSRouteUnknown, "rfbs_hybrid_destination_required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision, err := InspectRFBSOperation(tc.posting(), RFBSInput{Operation: RFBSMarkDelivering}, rfbsTestNow)
			if err != nil || decision.Route != tc.route || decision.Allowed {
				t.Fatalf("unexpected route or authority: route=%s allowed=%v error=%v", decision.Route, decision.Allowed, err)
			}
			if tc.code != "" && !rfbsHasDiagnostic(decision.Diagnostics, tc.code) {
				t.Fatalf("missing %s: %+v", tc.code, decision.Diagnostics)
			}
		})
	}
}

func TestRFBSProfileFreshnessAndIdentityAreIndependentPrerequisites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, code string
		mutate     func(*orders.Posting)
	}{
		{"warehouse mismatch", "rfbs_warehouse_profile_mismatch", func(p *orders.Posting) { p.WarehouseID = p.DeliveryMethodID }},
		{"method mismatch", "rfbs_delivery_method_profile_mismatch", func(p *orders.Posting) { p.DeliveryMethodID = p.WarehouseID }},
		{"expired exact boundary", "rfbs_delivery_capability_expired", func(p *orders.Posting) { p.DeliveryProfile.ValidUntil = rfbsTestNow }},
		{"old capability", "rfbs_delivery_capability_expired", func(p *orders.Posting) {
			p.DeliveryProfile.ObservedAt = rfbsTestNow.Add(-6 * time.Minute)
		}},
		{"source stale", "rfbs_source_not_fresh", func(p *orders.Posting) { p.Evidence.CheckedAt = rfbsTestNow.Add(-6 * time.Minute) }},
		{"express source conflict", "rfbs_standard_requires_non_express", func(p *orders.Posting) { p.Delivery.Express = orders.Present(true) }},
		{"scheme conflict", "rfbs_scheme_conflict", func(p *orders.Posting) { p.Scheme = orders.Present("FBS") }},
		{"missing carrier dimension", "rfbs_unverified_dimension", func(p *orders.Posting) {
			p.DeliveryProfile.VerifiedDimensions = []string{"scheme", "warehouse", "express", "destination"}
		}},
		{"destination conflict", "rfbs_destination_profile_mismatch", func(p *orders.Posting) { p.Analytics.DeliveryType = orders.Present("PVZ") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting := rfbsTestPosting()
			tc.mutate(&posting)
			decision, err := InspectRFBSOperation(posting, RFBSInput{Operation: RFBSMarkDelivering}, rfbsTestNow)
			if err != nil || decision.Allowed || !rfbsHasDiagnostic(decision.Diagnostics, tc.code) {
				t.Fatalf("profile prerequisite bypassed: allowed=%v error=%v diagnostics=%+v", decision.Allowed, err, decision.Diagnostics)
			}
		})
	}
}

func TestRFBSMalformedDeliveryFieldReturnsError(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	posting.Delivery.IntegrationType = orders.Field[string]{Presence: orders.PresenceValue}
	if _, err := InspectRFBSOperation(posting, RFBSInput{Operation: RFBSMarkDelivering}, rfbsTestNow); err == nil {
		t.Fatal("malformed known field accepted")
	}
}
