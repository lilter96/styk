package orderactions

import (
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

type rfbsTransition struct {
	endpoint     string
	predecessors []string
	result       string
}

// These predecessors are conservative local restrictions, not a verified Ozon
// transition contract. Every edge retains a workflow verification blocker.
func rfbsEdge(operation RFBSOperation) rfbsTransition {
	switch operation {
	case RFBSPack:
		return rfbsTransition{"/v4/posting/fbs/ship", []string{"awaiting_packaging"}, "awaiting_deliver"}
	case RFBSSetTracking:
		return rfbsTransition{"/v2/fbs/posting/tracking-number/set", []string{"awaiting_deliver", "delivering"}, ""}
	case RFBSMarkDelivering:
		return rfbsTransition{"/v2/fbs/posting/delivering", []string{"awaiting_deliver"}, "delivering"}
	case RFBSMarkLastMile:
		return rfbsTransition{"/v2/fbs/posting/last-mile", []string{"delivering"}, "driver_pickup"}
	case RFBSMarkDelivered:
		return rfbsTransition{"/v2/fbs/posting/delivered", []string{"driver_pickup"}, "delivered"}
	case RFBSSentBySeller:
		return rfbsTransition{endpoint: "/v2/fbs/posting/sent-by-seller"}
	case RFBSLabelCreate:
		return rfbsTransition{"/v3/posting/fbs/package-label/create", []string{"awaiting_deliver"}, ""}
	case RFBSLabelGet:
		return rfbsTransition{"/v2/posting/fbs/package-label/get", []string{"awaiting_deliver"}, ""}
	case RFBSCancel:
		return rfbsTransition{"/v2/posting/fbs/cancel", []string{"awaiting_packaging", "awaiting_deliver"}, "cancelled"}
	default:
		return rfbsTransition{}
	}
}

func rfbsOperationDiagnostics(posting orders.Posting, route RFBSRoute, input RFBSInput, edge rfbsTransition) []Diagnostic {
	diagnostics := []Diagnostic{rfbsDiagnostic("rfbs_transition_contract_unverified", "status")}
	diagnostics = append(diagnostics, rfbsStatusDiagnostics(posting.Status, edge)...)
	diagnostics = append(diagnostics, rfbsAvailableActionDiagnostics(posting.AvailableActions, input.ProviderAction)...)
	diagnostics = append(diagnostics, rfbsBoundaryDiagnostics(route, input.Operation)...)
	return append(diagnostics, rfbsRequiredInputDiagnostics(posting, route, input)...)
}

func rfbsStatusDiagnostics(status orders.Field[string], edge rfbsTransition) []Diagnostic {
	if !status.IsKnown() {
		return []Diagnostic{rfbsDiagnostic("rfbs_current_status_required", "status")}
	}
	known := []string{"awaiting_registration", "acceptance_in_progress", "awaiting_approve", "awaiting_packaging",
		"awaiting_deliver", "delivering", "driver_pickup", "delivered", "cancelled", "not_accepted", "arbitration", "client_arbitration"}
	if !slices.Contains(known, *status.Value) {
		return []Diagnostic{rfbsDiagnostic("rfbs_status_unknown", "status")}
	}
	if len(edge.predecessors) == 0 {
		return []Diagnostic{rfbsDiagnostic("rfbs_predecessor_contract_unverified", "status")}
	}
	if !slices.Contains(edge.predecessors, *status.Value) {
		return []Diagnostic{rfbsDiagnostic("rfbs_illegal_predecessor_status", "status")}
	}
	return nil
}

func rfbsAvailableActionDiagnostics(available []string, selected string) []Diagnostic {
	diagnostics := []Diagnostic{rfbsDiagnostic("rfbs_provider_action_mapping_unverified", "available_actions")}
	if available == nil {
		return append(diagnostics, rfbsDiagnostic("rfbs_available_actions_not_observed", "available_actions"))
	}
	if selected == "" {
		return append(diagnostics, rfbsDiagnostic("rfbs_provider_action_required", "available_actions"))
	}
	if !slices.Contains(available, selected) {
		return append(diagnostics, rfbsDiagnostic("rfbs_available_action_not_offered", "available_actions"))
	}
	return diagnostics
}

func rfbsAuthority(route RFBSRoute, operation RFBSOperation) string {
	if !rfbsStatusOperation(operation) {
		return "unverified"
	}
	if route == RFBSRouteIntegrated || route == RFBSRoutePartnerPVZ {
		return "carrier"
	}
	if route == RFBSRouteSellerPVZ && operation != RFBSMarkDelivering {
		return "ozon"
	}
	if route == RFBSRouteSeller || route == RFBSRouteSellerTracking || route == RFBSRouteSellerPVZ {
		return "seller"
	}
	return "unverified"
}

func rfbsStatusOperation(operation RFBSOperation) bool {
	return slices.Contains([]RFBSOperation{RFBSMarkDelivering, RFBSMarkLastMile, RFBSMarkDelivered, RFBSSentBySeller}, operation)
}

func rfbsBoundaryDiagnostics(route RFBSRoute, operation RFBSOperation) []Diagnostic {
	if route == RFBSRouteUnknown {
		return []Diagnostic{rfbsDiagnostic("rfbs_workflow_unclassified", "delivery_profile")}
	}
	if (route == RFBSRouteIntegrated || route == RFBSRoutePartnerPVZ) && (rfbsStatusOperation(operation) || operation == RFBSSetTracking) {
		return []Diagnostic{rfbsDiagnostic("rfbs_carrier_managed_action", "operation")}
	}
	if route == RFBSRouteSellerPVZ && rfbsStatusOperation(operation) && operation != RFBSMarkDelivering {
		return []Diagnostic{rfbsDiagnostic("rfbs_hybrid_later_statuses_automatic", "operation")}
	}
	if route == RFBSRouteSellerPVZ && operation == RFBSSetTracking {
		return []Diagnostic{rfbsDiagnostic("rfbs_hybrid_tracking_contract_unverified", "operation")}
	}
	return nil
}

func rfbsRequiredInputDiagnostics(posting orders.Posting, route RFBSRoute, input RFBSInput) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 3)
	if input.Operation == RFBSSetTracking && !input.TrackingNumber.IsKnown() {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_tracking_number_required", "tracking_number"))
	}
	if route == RFBSRouteSellerTracking && input.Operation == RFBSMarkDelivering && !posting.Delivery.TrackingNumber.IsKnown() {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_tracking_not_observed", "delivery.tracking_number"))
	}
	if route == RFBSRouteSellerPVZ && input.Operation == RFBSMarkDelivering {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_hybrid_label_evidence_required", "label"))
	}
	if input.Operation == RFBSCancel {
		if !input.ReasonID.IsKnown() {
			diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_cancellation_reason_required", "reason_id"))
		}
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_cancellation_reason_catalog_unverified", "reason_id"))
	}
	if input.Operation == RFBSPack {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_exemplar_requirements_unverified", "products"))
	}
	if input.Operation == RFBSMarkDelivered {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_delivery_confirmation_required", "delivery_confirmation"))
	}
	if input.Operation == RFBSSentBySeller {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_sent_by_seller_workflow_unverified", "operation"))
	}
	return diagnostics
}
