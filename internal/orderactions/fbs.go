package orderactions

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

// PlanFBS produces reviewable diagnostics without constructing an unverified
// provider payload. No current FBS schema in the manifest permits dispatch.
func PlanFBS(posting orders.Posting, command orders.Command) (RequestPlan, error) {
	plan := RequestPlan{Scope: command.Scope, Action: command.Action, CommandID: command.ID,
		PostingNumber: posting.PostingNumber, SourceRevision: posting.Evidence.Revision,
		Method: http.MethodPost, Diagnostics: make([]Diagnostic, 0, 12)}
	if fbsKnownString(posting.Scheme, "FBO") {
		plan.Diagnostics = append(plan.Diagnostics, Diagnostic{Severity: "error", Code: "fbo_excluded", Field: "scheme"})
		return plan, orders.ErrBlocked
	}
	if err := validateFBSIntent(posting, command); err != nil {
		plan.Diagnostics = append(plan.Diagnostics, fbsIntentDiagnostic(err))
		return plan, err
	}
	operation, known := fbsCommandOperation(command.Action)
	if !known {
		plan.Diagnostics = append(plan.Diagnostics, Diagnostic{Severity: "error", Code: "unknown_fbs_command", Field: "action"})
		return plan, orders.ErrBlocked
	}
	node, _ := fbsNode(operation)
	plan.Endpoint = node.Endpoint
	plan.Diagnostics = append(plan.Diagnostics, AssessFBSOperation(operation)...)
	plan.Diagnostics = append(plan.Diagnostics, ValidateFBSProfile(posting, command.PostingAction.Capability, time.Now().UTC())...)
	plan.Diagnostics = append(plan.Diagnostics, fbsActionDiagnostics(posting, command, operation)...)
	if err := fbsContractDiagnostic(&plan); err != nil {
		return plan, err
	}
	return plan, fmt.Errorf("plan FBS action with unverified contract: %w", orders.ErrBlocked)
}

func fbsIntentDiagnostic(err error) Diagnostic {
	code := "fbs_source_or_scheme_unverified"
	if errors.Is(err, orders.ErrStale) {
		code = "fbs_intent_stale"
	}
	if errors.Is(err, orders.ErrInvalid) {
		code = "fbs_intent_invalid"
	}
	return Diagnostic{Severity: "error", Code: code, Field: "command"}
}

func validateFBSIntent(posting orders.Posting, command orders.Command) error {
	if err := posting.Validate(); err != nil {
		return fmt.Errorf("validate FBS posting: %w", err)
	}
	if err := command.Validate(); err != nil {
		return fmt.Errorf("validate FBS command: %w", err)
	}
	if posting.Scope != command.Scope || posting.PostingNumber != command.PostingNumber || posting.CommercialOrderID != command.CommercialOrderID {
		return orders.ErrStale
	}
	if posting.Evidence.Revision != command.ExpectedSourceRevision {
		return orders.ErrStale
	}
	if !posting.Scheme.IsKnown() || *posting.Scheme.Value != "FBS" || posting.Evidence.Origin != orders.OriginOzon {
		return orders.ErrBlocked
	}
	if command.PostingAction == nil || command.CustomerOrder != nil {
		return orders.ErrInvalid
	}
	return nil
}

func fbsCommandOperation(action orders.Action) (FBSOperation, bool) {
	switch action {
	case orders.ActionPostingPack:
		return FBSPack, true
	case orders.ActionPostingShip:
		return FBSShip, true
	case orders.ActionPostingCancel:
		return FBSCancel, true
	default:
		return "", false
	}
}

func fbsContractDiagnostic(plan *RequestPlan) error {
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		return fmt.Errorf("load FBS contracts: %w", err)
	}
	for _, contract := range manifest.Contracts {
		if contract.Endpoint == plan.Endpoint && contract.Direction == "request" {
			plan.ContractHash = contract.SHA256
			return nil
		}
	}
	plan.Diagnostics = append(plan.Diagnostics, Diagnostic{Severity: "error", Code: "request_contract_missing", Field: "contract"})
	return nil
}

// ValidateFBSProfile binds all reviewed dimensions and both provider identity
// spaces. available_actions is optional evidence and cannot prove capability.
func ValidateFBSProfile(posting orders.Posting, capability orders.CapabilityRef, now time.Time) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 8)
	if capability.Validate() != nil || posting.DeliveryProfile == nil {
		return append(diagnostics, Diagnostic{Severity: "error", Code: "reviewed_profile_missing", Field: "delivery_profile"})
	}
	profile := posting.DeliveryProfile
	if profile.Validate() != nil || !fbsSameProfile(*profile, capability) || !fbsProfileMatchesPosting(posting, capability) {
		diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "reviewed_profile_mismatch", Field: "delivery_profile"})
	}
	if now.IsZero() || capability.ObservedAt.After(now) || !capability.ValidUntil.After(now) || profile.ObservedAt.After(now) || !profile.ValidUntil.After(now) {
		diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "reviewed_profile_expired", Field: "delivery_profile"})
	}
	for _, dimension := range []string{"scheme", "warehouse", "carrier", "express", "destination"} {
		if !slices.Contains(capability.VerifiedDimensions, dimension) || !slices.Contains(profile.VerifiedDimensions, dimension) {
			diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "unverified_" + dimension, Field: "delivery_profile"})
		}
	}
	diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "provider_permissions_unverified", Field: "capability"})
	if posting.AvailableActions == nil {
		return append(diagnostics, Diagnostic{Severity: "warning", Code: "available_actions_absent", Field: "available_actions"})
	}
	diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "available_actions_contract_unverified", Field: "available_actions"})
	if len(posting.AvailableActions) > 0 {
		diagnostics = append(diagnostics, Diagnostic{Severity: "error", Code: "available_action_enum_unverified", Field: "available_actions"})
	}
	return diagnostics
}

func fbsSameProfile(source, command orders.CapabilityRef) bool {
	return source.ID == command.ID && source.Revision == command.Revision &&
		source.WarehouseID == command.WarehouseID && source.DeliveryMethodID == command.DeliveryMethodID &&
		source.Scheme == command.Scheme && fbsStringEqual(source.Carrier, command.Carrier) &&
		fbsStringEqual(source.Destination, command.Destination) && source.Express.IsKnown() && command.Express.IsKnown() &&
		source.Express.Validate() == nil && command.Express.Validate() == nil && *source.Express.Value == *command.Express.Value
}

func fbsProfileMatchesPosting(posting orders.Posting, capability orders.CapabilityRef) bool {
	return capability.Scheme == "FBS" && fbsKnownString(posting.Scheme, "FBS") &&
		fbsKnownString(posting.WarehouseID, capability.WarehouseID) && fbsKnownString(posting.DeliveryMethodID, capability.DeliveryMethodID) &&
		fbsKnownString(capability.Carrier, "ozon") && fbsKnownString(capability.Destination, "customer") &&
		capability.Express.Validate() == nil && capability.Express.IsKnown() && !*capability.Express.Value
}

func fbsKnownString(field orders.Field[string], expected string) bool {
	return field.Validate() == nil && field.IsKnown() && *field.Value == expected
}

func fbsStringEqual(left, right orders.Field[string]) bool {
	return left.Validate() == nil && right.Validate() == nil && left.IsKnown() && right.IsKnown() && *left.Value == *right.Value
}

func fbsActionDiagnostics(posting orders.Posting, command orders.Command, operation FBSOperation) []Diagnostic {
	result := make([]Diagnostic, 0, 5)
	if !fbsKnownStatus(posting.Status) {
		result = append(result, Diagnostic{Severity: "error", Code: "unknown_fbs_status", Field: "status"})
	}
	if operation == FBSPack && !fbsKnownString(posting.Status, "awaiting_packaging") {
		result = append(result, Diagnostic{Severity: "error", Code: "pack_status_not_awaiting_packaging", Field: "status"})
	}
	if operation == FBSPack {
		result = append(result, Diagnostic{Severity: "error", Code: "package_instance_input_missing", Field: "posting_action"})
	}
	if operation == FBSCancel {
		result = append(result, Diagnostic{Severity: "error", Code: "cancellation_reason_catalogue_unverified", Field: "posting_action.reason_id"})
		if !command.PostingAction.ReasonID.IsKnown() {
			result = append(result, Diagnostic{Severity: "error", Code: "cancellation_reason_id_missing", Field: "posting_action.reason_id"})
		}
		if len(command.PostingAction.Lines) > 0 {
			result = append(result, Diagnostic{Severity: "error", Code: "partial_cancellation_quantities_missing", Field: "posting_action.lines"})
		}
	}
	return result
}

func fbsKnownStatus(status orders.Field[string]) bool {
	if status.Validate() != nil || !status.IsKnown() {
		return false
	}
	return slices.Contains([]string{"awaiting_packaging", "awaiting_deliver", "acceptance_in_progress", "delivering", "delivered", "cancelled", "arbitration", "not_accepted"}, *status.Value)
}
