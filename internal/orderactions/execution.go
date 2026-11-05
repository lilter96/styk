package orderactions

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

// ContractVersion records the operation subset reviewed against the official
// Chinese Swagger in the preceding lab. It is not a full Swagger download.
const ContractVersion = "ozon-fulfillment-2026-11-04:v4:ship-v4,cancel-v2,partial-result-string,tracking-v2,delivery-v2,label-v3,hybrid-printed-label-v1,marking-requirements-v1,exemplars-v6-v5-v1,country-v2-v1,method-integration-v1,explicit-express-action-v1"

//go:embed label_contract.json
var labelContract []byte

//go:embed requirements_contract.json
var requirementsContract []byte

//go:embed exemplar_contract.json
var exemplarContract []byte

//go:embed country_contract.json
var countryContract []byte

//go:embed delivery_method_contract.json
var deliveryMethodContract []byte

func ExecutionContractHash() string {
	payload := append([]byte(ContractVersion), labelContract...)
	payload = append(payload, requirementsContract...)
	payload = append(payload, exemplarContract...)
	payload = append(payload, countryContract...)
	payload = append(payload, ordercontract.UnassembledSplitContractHash()...)
	sum := sha256.Sum256(append(payload, deliveryMethodContract...))
	return hex.EncodeToString(sum[:])
}

func ExecutionActions() []orders.Action {
	return []orders.Action{orders.ActionCountrySet, orders.ActionExemplarPrepare, orders.ActionExemplarSet, orders.ActionPostingPack, orders.ActionPostingSplit, orders.ActionPostingSplitUnassembled, orders.ActionPostingCancel,
		orders.ActionPostingPartialCancel, orders.ActionPostingTracking, orders.ActionPostingShip,
		orders.ActionPostingLastMile, orders.ActionPostingDeliver, orders.ActionPostingLabelCreate}
}

// RequiredParameters returns the reviewed inputs a fulfillment action needs
// before it can be proposed. It never infers provider evidence.
func RequiredParameters(action orders.Action) []string {
	return slices.Clone(executionEdge(action).ParametersRequired)
}

func PlanExecution(posting orders.Posting, cap orders.CapabilityRef, action orders.Action, now time.Time) orders.ActionOption {
	return PlanExecutionWithProof(posting, cap, action, orders.ExemplarEvidence{}, now)
}

func PlanExecutionWithProof(posting orders.Posting, cap orders.CapabilityRef, action orders.Action, proof orders.ExemplarEvidence, now time.Time) orders.ActionOption {
	plan := executionEdge(action)
	if proof.Verified && (action == orders.ActionPostingPack || action == orders.ActionPostingSplit) {
		plan.ParametersRequired = append(plan.ParametersRequired, "exemplar_work_id")
	}
	if orders.HybridLabelRequired(posting, action) {
		plan.ParametersRequired = append(plan.ParametersRequired, "label_work_id", "label_printed_confirmed")
	}
	plan.Blockers = append(plan.Blockers, executionFacts(posting, cap, now)...)
	if action == orders.ActionCountrySet {
		if required, err := orders.CountryRequiredSKUs(posting); err != nil || len(required) == 0 {
			plan.Blockers = append(plan.Blockers, actionBlock("requirements.country", "country_requirement_not_observed"))
		}
	}
	plan.Blockers = append(plan.Blockers, PackingRequirementValidationWithProof(posting, action, proof, now)...)
	if !posting.Status.IsKnown() || !executionPredecessor(action, *posting.Status.Value) {
		plan.Blockers = append(plan.Blockers, actionBlock("status", "action_not_available_in_current_status"))
	}
	if !actionAdvertised(posting.AvailableActions, action) {
		plan.Blockers = append(plan.Blockers, actionBlock("available_actions", "provider_does_not_offer_action"))
	}
	if slices.Contains([]orders.Action{orders.ActionPostingShip, orders.ActionPostingLastMile, orders.ActionPostingDeliver}, action) {
		plan.Blockers = append(plan.Blockers, sellerDeliveryControl(posting, cap, action)...)
	}
	if action == orders.ActionPostingTracking && cap.Scheme != "rFBS" {
		plan.Blockers = append(plan.Blockers, actionBlock("scheme", "tracking_requires_rfbs"))
	}
	plan.Allowed = len(plan.Blockers) == 0
	return plan
}

func executionFacts(posting orders.Posting, cap orders.CapabilityRef, now time.Time) []orders.Validation {
	if orders.DeliveryIntegrationConflict(posting) {
		return []orders.Validation{actionBlock("delivery.integration_type", "conflicting_integration")}
	}
	if posting.Validate() != nil || cap.Validate() != nil || posting.Evidence.Origin != orders.OriginOzon || !posting.WarehouseID.IsKnown() || *posting.WarehouseID.Value != cap.WarehouseID {
		return []orders.Validation{actionBlock("posting", "exact_posting_capability_required")}
	}
	if !slices.Contains([]string{"FBS", "rFBS"}, cap.Scheme) || (posting.Scheme.IsKnown() && *posting.Scheme.Value != cap.Scheme) {
		return []orders.Validation{actionBlock("scheme", "seller_fulfilled_profile_required")}
	}
	if now.Before(cap.ObservedAt) || !now.Before(cap.ValidUntil) || !slices.Contains(cap.VerifiedDimensions, "scheme") || !slices.Contains(cap.VerifiedDimensions, "warehouse") {
		return []orders.Validation{actionBlock("delivery_profile", "fresh_warehouse_capability_required")}
	}
	if !posting.DeliveryMethodID.IsKnown() || *posting.DeliveryMethodID.Value != cap.DeliveryMethodID {
		return []orders.Validation{actionBlock("delivery_method_id", "exact_delivery_method_required")}
	}
	return nil
}

func executionEdge(action orders.Action) orders.ActionOption {
	p := orders.ActionOption{Action: action, Blockers: []orders.Validation{}, ParametersRequired: []string{}}
	switch action {
	case orders.ActionCountrySet:
		p.Endpoint, p.ParametersRequired = "/v2/posting/fbs/product/country/set", []string{"country_sku", "country_iso_code", "country_confirmed"}
	case orders.ActionExemplarPrepare:
		p.Endpoint = "/v6/fbs/posting/product/exemplar/create-or-get"
	case orders.ActionExemplarSet:
		p.Endpoint, p.ParametersRequired = "/v6/fbs/posting/product/exemplar/set", []string{"exemplar_work_id", "exemplars"}
	case orders.ActionPostingPack, orders.ActionPostingSplit:
		p.Endpoint, p.ExpectedStatus, p.ParametersRequired = "/v4/posting/fbs/ship", "awaiting_deliver", []string{"packages", "readiness_confirmed"}
	case orders.ActionPostingSplitUnassembled:
		p.Endpoint, p.ParametersRequired = "/v1/posting/fbs/split", []string{"packages"}
	case orders.ActionPostingCancel:
		p.Endpoint, p.ExpectedStatus, p.ParametersRequired = "/v2/posting/fbs/cancel", "cancelled", []string{"reason_id"}
	case orders.ActionPostingPartialCancel:
		p.Endpoint, p.ParametersRequired = "/v2/posting/fbs/product/cancel", []string{"reason_id", "reason_message", "lines"}
	case orders.ActionPostingTracking:
		p.Endpoint, p.ParametersRequired = "/v2/fbs/posting/tracking-number/set", []string{"tracking_number"}
	case orders.ActionPostingLabelCreate:
		p.Endpoint, p.ParametersRequired = "/v3/posting/fbs/package-label/create", []string{"label_type"}
	case orders.ActionPostingShip:
		p.Endpoint, p.ExpectedStatus, p.ParametersRequired = "/v2/fbs/posting/delivering", "delivering", []string{"event_confirmed", "event_reference"}
	case orders.ActionPostingLastMile:
		p.Endpoint, p.ExpectedStatus, p.ParametersRequired = "/v2/fbs/posting/last-mile", "driver_pickup", []string{"event_confirmed", "event_reference"}
	case orders.ActionPostingDeliver:
		p.Endpoint, p.ExpectedStatus, p.ParametersRequired = "/v2/fbs/posting/delivered", "delivered", []string{"event_confirmed", "event_reference"}
	case orders.ActionPostingSentBySeller:
		// Official notice 2026-01-20 retires this method. Historical actions
		// remain readable; the notice establishes no replacement or disable date.
		p.Blockers = append(p.Blockers, actionBlock("action", "provider_method_deprecated"))
	default:
		p.Blockers = append(p.Blockers, actionBlock("action", "operation_contract_not_verified"))
	}
	return p
}

func executionPredecessor(action orders.Action, status string) bool {
	switch action {
	case orders.ActionCountrySet, orders.ActionExemplarPrepare, orders.ActionExemplarSet:
		return status == "awaiting_packaging"
	case orders.ActionPostingPack, orders.ActionPostingSplit, orders.ActionPostingSplitUnassembled:
		return status == "awaiting_packaging"
	case orders.ActionPostingCancel, orders.ActionPostingPartialCancel:
		return slices.Contains([]string{"awaiting_packaging", "awaiting_deliver"}, status)
	case orders.ActionPostingTracking:
		return status == "awaiting_deliver" || status == "delivering"
	case orders.ActionPostingShip, orders.ActionPostingLabelCreate:
		return status == "awaiting_deliver"
	case orders.ActionPostingLastMile:
		return status == "delivering"
	case orders.ActionPostingDeliver:
		return status == "driver_pickup"
	default:
		return false
	}
}

func sellerDeliveryControl(posting orders.Posting, cap orders.CapabilityRef, action orders.Action) []orders.Validation {
	if cap.Scheme != "rFBS" || !cap.Carrier.IsKnown() || *cap.Carrier.Value != "non_integrated" || !slices.Contains(cap.VerifiedDimensions, "carrier") {
		return []orders.Validation{actionBlock("delivery.carrier", "delivery_transition_managed_by_carrier_or_ozon")}
	}
	if !posting.Delivery.IntegrationType.IsKnown() || !slices.Contains([]string{"non_integrated", "3pl_tracking", "hybrid"}, *posting.Delivery.IntegrationType.Value) {
		return []orders.Validation{actionBlock("delivery.integration_type", "seller_delivery_control_not_verified")}
	}
	if validation := explicitDeliveryProfileAction(posting, cap, action); validation.Code != "" {
		return []orders.Validation{validation}
	}
	if *posting.Delivery.IntegrationType.Value == "hybrid" && action != orders.ActionPostingShip {
		return []orders.Validation{actionBlock("delivery.destination", "pvz_last_mile_managed_by_ozon")}
	}
	return nil
}

func actionAdvertised(actions []string, action orders.Action) bool {
	if action == orders.ActionPostingSplitUnassembled {
		// The optional list has no documented token for this distinct endpoint.
		// Do not fabricate one or treat the assembly token as authorization.
		return true
	}
	if action == orders.ActionCountrySet {
		return true
	} // Required country facts, not an undocumented available_actions token, gate this operation.
	if actions == nil {
		return true // the optional list is not exhaustive when absent
	}
	tokens := map[orders.Action][]string{
		orders.ActionExemplarPrepare: {"update_cis", "ship_with_additional_info", "ship", "ship_async"}, orders.ActionExemplarSet: {"update_cis", "ship_with_additional_info", "ship", "ship_async"},
		orders.ActionPostingPack: {"ship", "ship_async", "pack"}, orders.ActionPostingSplit: {"ship", "ship_async"},
		orders.ActionPostingCancel: {"cancel"}, orders.ActionPostingPartialCancel: {"product_cancel"},
		orders.ActionPostingTracking: {"set_track_number"}, orders.ActionPostingShip: {"non_int_delivering"},
		orders.ActionPostingLastMile: {"non_int_last_mile"}, orders.ActionPostingDeliver: {"non_int_delivered"},
		orders.ActionPostingLabelCreate: {"label_download", "label_download_big", "label_download_small"},
	}
	return slices.ContainsFunc(tokens[action], func(token string) bool { return slices.Contains(actions, token) })
}

func actionBlock(field, code string) orders.Validation {
	return orders.Validation{Field: field, Code: code}
}
