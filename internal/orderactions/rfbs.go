package orderactions

import (
	"fmt"
	"reflect"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

func PlanRFBS(posting orders.Posting, command orders.Command) (RequestPlan, error) {
	return PlanRFBSAt(posting, command, time.Now().UTC())
}

// PlanRFBSAt binds diagnostics to the exact reviewed command and current source.
// No request body or dispatch permission exists for an unverified schema.
func PlanRFBSAt(posting orders.Posting, command orders.Command, now time.Time) (RequestPlan, error) {
	plan := RequestPlan{Scope: command.Scope, Action: command.Action, CommandID: command.ID,
		PostingNumber: command.PostingNumber, SourceRevision: command.ExpectedSourceRevision, Method: "POST"}
	if err := command.Validate(); err != nil {
		return plan, fmt.Errorf("validate rFBS command: %w", err)
	}
	if !rfbsCommandMatches(posting, command) {
		return plan, orders.ErrStale
	}
	operation, ok := rfbsCommandOperation(command.Action)
	if !ok {
		return plan, orders.ErrBlocked
	}
	input := RFBSInput{Operation: operation, ReasonID: command.PostingAction.ReasonID}
	decision, err := InspectRFBSOperation(posting, input, now)
	if err != nil {
		return plan, err
	}
	plan.Endpoint, plan.ContractHash, plan.Diagnostics = decision.Endpoint, decision.ContractHash, decision.Diagnostics
	plan.Diagnostics = append(plan.Diagnostics, rfbsDiagnostic("rfbs_durable_action_contract_unverified", "posting_action"))
	return plan, fmt.Errorf("plan rFBS action with unverified contract: %w", orders.ErrBlocked)
}

func rfbsCommandMatches(posting orders.Posting, command orders.Command) bool {
	if command.Scope != posting.Scope || command.PostingNumber != posting.PostingNumber || command.CommercialOrderID != posting.CommercialOrderID {
		return false
	}
	if command.ExpectedSourceRevision != posting.Evidence.Revision || command.PostingAction == nil || posting.DeliveryProfile == nil {
		return false
	}
	capability := command.PostingAction.Capability
	return reflect.DeepEqual(capability, *posting.DeliveryProfile) && reflect.DeepEqual(capability, command.Warehouse.Capability)
}

func rfbsCommandOperation(action orders.Action) (RFBSOperation, bool) {
	switch action {
	case orders.ActionPostingPack:
		return RFBSPack, true
	case orders.ActionPostingShip:
		return RFBSMarkDelivering, true
	case orders.ActionPostingDeliver:
		return RFBSMarkDelivered, true
	case orders.ActionPostingCancel:
		return RFBSCancel, true
	default:
		return "", false
	}
}

// RFBSStandardGraph inspects every operation independently. Unknown source
// statuses or action tokens affect this posting, without changing other targets.
func RFBSStandardGraph(posting orders.Posting, now time.Time) ([]RFBSActionDecision, error) {
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		return nil, err
	}
	operations := []RFBSOperation{RFBSPack, RFBSLabelCreate, RFBSLabelGet, RFBSSetTracking,
		RFBSMarkDelivering, RFBSMarkLastMile, RFBSMarkDelivered, RFBSSentBySeller, RFBSCancel}
	decisions := make([]RFBSActionDecision, 0, len(operations))
	for _, operation := range operations {
		decision, inspectErr := inspectRFBS(posting, RFBSInput{Operation: operation}, now, manifest)
		if inspectErr != nil {
			return nil, inspectErr
		}
		decisions = append(decisions, decision)
	}
	return decisions, nil
}

func InspectRFBSOperation(posting orders.Posting, input RFBSInput, now time.Time) (RFBSActionDecision, error) {
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		return RFBSActionDecision{}, err
	}
	return inspectRFBS(posting, input, now, manifest)
}

func inspectRFBS(posting orders.Posting, input RFBSInput, now time.Time, manifest ordercontract.Manifest) (RFBSActionDecision, error) {
	if posting.Validate() != nil || !rfbsDeliveryValid(posting.Delivery) || now.IsZero() || !input.Operation.valid() || !rfbsInputValid(input) {
		return RFBSActionDecision{}, orders.ErrInvalid
	}
	route, diagnostics := rfbsRoute(posting, now)
	edge := rfbsEdge(input.Operation)
	decision := RFBSActionDecision{Operation: input.Operation, Route: route, Endpoint: edge.endpoint,
		CurrentStatus: posting.Status, ExpectedStatus: edge.result, Authority: rfbsAuthority(route, input.Operation), Diagnostics: diagnostics}
	hash, contractDiagnostics, err := rfbsContract(manifest, edge.endpoint)
	if err != nil {
		return RFBSActionDecision{}, err
	}
	decision.ContractHash = hash
	decision.Diagnostics = append(decision.Diagnostics, contractDiagnostics...)
	decision.Diagnostics = append(decision.Diagnostics, rfbsOperationDiagnostics(posting, route, input, edge)...)
	return decision, nil
}
