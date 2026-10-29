package orderactions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestRFBSHybridSellerStopsManualProgressAfterDelivering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		operation RFBSOperation
		status    string
		code      string
		authority string
	}{
		{RFBSMarkDelivering, "awaiting_deliver", "rfbs_hybrid_label_evidence_required", "seller"},
		{RFBSMarkLastMile, "delivering", "rfbs_hybrid_later_statuses_automatic", "ozon"},
		{RFBSMarkDelivered, "driver_pickup", "rfbs_hybrid_later_statuses_automatic", "ozon"},
		{RFBSSentBySeller, "awaiting_deliver", "rfbs_hybrid_later_statuses_automatic", "ozon"},
	}
	for _, tc := range tests {
		t.Run(string(tc.operation), func(t *testing.T) {
			t.Parallel()
			posting := rfbsPVZPosting(false)
			posting.Status = orders.Present(tc.status)
			decision, err := InspectRFBSOperation(posting, RFBSInput{Operation: tc.operation}, rfbsTestNow)
			if err != nil || decision.Allowed || decision.Authority != tc.authority || !rfbsHasDiagnostic(decision.Diagnostics, tc.code) {
				t.Fatalf("hybrid authority boundary lost: %+v error=%v", decision, err)
			}
		})
	}
}

func TestRFBSIntegratedCarrierCannotBeOverriddenByAvailableAction(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	posting.DeliveryProfile.Carrier = orders.Present("integrated")
	posting.Delivery.IntegrationType = orders.Present("unverified_partner_enum")
	decision, err := InspectRFBSOperation(posting, RFBSInput{Operation: RFBSMarkDelivering, ProviderAction: "LOCAL_FIXTURE_ACTION"}, rfbsTestNow)
	if err != nil || decision.Route != RFBSRouteIntegrated || decision.Authority != "carrier" || decision.Allowed {
		t.Fatalf("unexpected integrated decision: %+v error=%v", decision, err)
	}
	if !rfbsHasDiagnostic(decision.Diagnostics, "rfbs_carrier_managed_action") || !rfbsHasDiagnostic(decision.Diagnostics, "rfbs_integration_enum_unverified") {
		t.Fatal("unverified integration or manual carrier override was accepted")
	}
}

func TestRFBSPredecessorsCannotSkipOrReopenDelivery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, status, code string
		operation          RFBSOperation
	}{
		{"pack after shipping", "delivering", "rfbs_illegal_predecessor_status", RFBSPack},
		{"deliver skips last mile", "awaiting_deliver", "rfbs_illegal_predecessor_status", RFBSMarkDelivered},
		{"last mile after completed", "delivered", "rfbs_illegal_predecessor_status", RFBSMarkLastMile},
		{"unknown status", "future_provider_status", "rfbs_status_unknown", RFBSMarkDelivering},
		{"sent by seller contract absent", "awaiting_deliver", "rfbs_predecessor_contract_unverified", RFBSSentBySeller},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting := rfbsTestPosting()
			posting.Status = orders.Present(tc.status)
			decision, err := InspectRFBSOperation(posting, RFBSInput{Operation: tc.operation}, rfbsTestNow)
			if err != nil || !rfbsHasDiagnostic(decision.Diagnostics, tc.code) || decision.Allowed {
				t.Fatalf("unsafe predecessor accepted: %+v error=%v", decision, err)
			}
		})
	}
}

func TestRFBSTrackingNeverUsesEmptyOrFabricatedFallback(t *testing.T) {
	t.Parallel()
	for _, presence := range []orders.Presence{orders.PresenceMissing, orders.PresenceNull, orders.PresenceEmpty} {
		t.Run(string(presence), func(t *testing.T) {
			t.Parallel()
			input := RFBSInput{Operation: RFBSSetTracking, TrackingNumber: orders.Field[string]{Presence: presence}}
			decision, err := InspectRFBSOperation(rfbsTestPosting(), input, rfbsTestNow)
			if err != nil || !rfbsHasDiagnostic(decision.Diagnostics, "rfbs_tracking_number_required") || decision.Allowed {
				t.Fatalf("missing tracking accepted: %+v error=%v", decision, err)
			}
		})
	}
	input := RFBSInput{Operation: RFBSSetTracking, TrackingNumber: orders.Present("SENSITIVE_TRACKING_REFERENCE")}
	decision, err := InspectRFBSOperation(rfbsTestPosting(), input, rfbsTestNow)
	if err != nil || rfbsHasDiagnostic(decision.Diagnostics, "rfbs_tracking_number_required") || decision.Allowed {
		t.Fatalf("explicit tracking lost or authorized unverified request: %+v error=%v", decision, err)
	}
	raw, err := json.Marshal(decision)
	if err != nil || strings.Contains(string(raw), "SENSITIVE") {
		t.Fatal("tracking reference leaked into diagnostics")
	}
}

func TestRFBSAvailableActionSelectionPreservesExactSourceList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		available []string
		selected  string
		code      string
	}{
		{"missing", nil, "", "rfbs_available_actions_not_observed"},
		{"empty explicitly denies", []string{}, "LOCAL_FIXTURE_ACTION", "rfbs_available_action_not_offered"},
		{"no command token", []string{"LOCAL_FIXTURE_ACTION"}, "", "rfbs_provider_action_required"},
		{"different token", []string{"LOCAL_FIXTURE_ACTION"}, "different", "rfbs_available_action_not_offered"},
		{"matching token still unverified", []string{"LOCAL_FIXTURE_ACTION", "unknown_unrelated"}, "LOCAL_FIXTURE_ACTION", "rfbs_provider_action_mapping_unverified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting := rfbsTestPosting()
			posting.AvailableActions = tc.available
			decision, err := InspectRFBSOperation(posting, RFBSInput{Operation: RFBSMarkDelivering, ProviderAction: tc.selected}, rfbsTestNow)
			if err != nil || !rfbsHasDiagnostic(decision.Diagnostics, tc.code) || decision.Allowed {
				t.Fatalf("source action list ignored: %+v error=%v", decision, err)
			}
		})
	}
}

func TestRFBSGraphDoesNotTreatObservedStatusesAsCommands(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	posting.Status = orders.Present("delivered")
	graph, err := RFBSStandardGraph(posting, rfbsTestNow)
	if err != nil || len(graph) != 9 {
		t.Fatalf("missing operation inspection: len=%d error=%v", len(graph), err)
	}
	for _, decision := range graph {
		if decision.Allowed || !rfbsHasDiagnostic(decision.Diagnostics, "rfbs_transition_contract_unverified") {
			t.Fatalf("observed status authorized an operation: %+v", decision)
		}
	}
	posting.Status = orders.Present("awaiting_deliver")
	decision, err := InspectRFBSOperation(posting, RFBSInput{Operation: RFBSMarkDelivering}, rfbsTestNow)
	if err != nil || rfbsHasDiagnostic(decision.Diagnostics, "rfbs_illegal_predecessor_status") {
		t.Fatal("one target's previous state affected another inspection")
	}
}

func TestRFBSMissingReasonAndExemplarContractAreVisible(t *testing.T) {
	t.Parallel()
	tests := []struct {
		operation RFBSOperation
		code      string
	}{
		{RFBSCancel, "rfbs_cancellation_reason_required"},
		{RFBSPack, "rfbs_exemplar_requirements_unverified"},
		{RFBSMarkDelivered, "rfbs_delivery_confirmation_required"},
	}
	for _, tc := range tests {
		t.Run(string(tc.operation), func(t *testing.T) {
			t.Parallel()
			decision, err := InspectRFBSOperation(rfbsTestPosting(), RFBSInput{Operation: tc.operation}, rfbsTestNow)
			if err != nil || !rfbsHasDiagnostic(decision.Diagnostics, tc.code) || decision.Allowed {
				t.Fatalf("required operation evidence hidden: %+v error=%v", decision, err)
			}
		})
	}
}
