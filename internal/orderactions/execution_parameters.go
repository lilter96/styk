package orderactions

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func ValidateExecution(posting orders.Posting, cap orders.CapabilityRef, proposal orders.ActionProposal, reasons []orders.ActionReason, now time.Time) []orders.Validation {
	return ValidateExecutionWithProof(posting, cap, proposal, reasons, orders.ExemplarEvidence{}, now)
}

func ValidateExecutionWithProof(posting orders.Posting, cap orders.CapabilityRef, proposal orders.ActionProposal, reasons []orders.ActionReason, proof orders.ExemplarEvidence, now time.Time) []orders.Validation {
	plan := PlanExecutionWithProof(posting, cap, proposal.Action, proof, now)
	if proof.Verified && (proposal.Action == orders.ActionPostingPack || proposal.Action == orders.ActionPostingSplit) && proposal.Parameters.ExemplarWorkID != proof.SnapshotID {
		return []orders.Validation{actionBlock("exemplar_work_id", "exemplar_verified_proof_required")}
	}
	if proposal.PostingNumber != posting.PostingNumber {
		return append(plan.Blockers, actionBlock("posting_number", "exact_posting_required"))
	}
	if len(plan.Blockers) > 0 {
		return plan.Blockers
	}
	if err := executionInput(posting, proposal.Action, proposal.Parameters, reasons); err != nil {
		return []orders.Validation{actionBlock("parameters", "required_action_values_invalid_or_missing")}
	}
	return nil
}

func executionInput(posting orders.Posting, action orders.Action, input orders.ActionParameters, reasons []orders.ActionReason) error {
	switch action {
	case orders.ActionExemplarPrepare:
		return nil
	case orders.ActionExemplarSet:
		if input.ExemplarWorkID == "" || len(input.ExemplarWorkID) > 256 || input.Exemplars == nil {
			return orders.ErrInvalid
		}
		return nil
	case orders.ActionPostingPack, orders.ActionPostingSplit:
		if !input.ReadinessConfirmed || (action == orders.ActionPostingSplit && len(input.Packages) < 2) || (action == orders.ActionPostingPack && len(input.Packages) != 1) {
			return orders.ErrBlocked
		}
		return validatePackageConservation(posting, input.Packages)
	case orders.ActionPostingCancel, orders.ActionPostingPartialCancel:
		if !positiveSKU(input.ReasonID) || !slices.ContainsFunc(reasons, func(reason orders.ActionReason) bool { return reason.ID == input.ReasonID }) || (input.ReasonID == "402" && !executionText(input.ReasonMessage)) {
			return orders.ErrBlocked
		}
		if action == orders.ActionPostingPartialCancel {
			return validatePartialQuantities(posting, input)
		}
	case orders.ActionPostingTracking:
		if !executionText(input.TrackingNumber) || len(input.TrackingNumber) > 128 {
			return orders.ErrInvalid
		}
	case orders.ActionPostingShip, orders.ActionPostingLastMile, orders.ActionPostingDeliver:
		if !input.EventConfirmed || !executionText(input.EventReference) {
			return orders.ErrBlocked
		}
		if orders.HybridLabelRequired(posting, action) && (!input.LabelPrintedConfirmed || !executionText(input.LabelWorkID) || len(input.LabelWorkID) > 256) {
			return orders.ErrBlocked
		}
	case orders.ActionPostingLabelCreate:
		if !slices.Contains([]string{"big_label", "small_label"}, input.LabelType) {
			return orders.ErrInvalid
		}
	default:
		return orders.ErrBlocked
	}
	return nil
}

func currentPostingUnits(posting orders.Posting) (map[string]uint64, error) {
	units := make(map[string]uint64, len(posting.Lines))
	if len(posting.Lines) == 0 || len(posting.Lines) > maxFBSLines {
		return nil, orders.ErrInvalid
	}
	for _, line := range posting.Lines {
		quantity, err := fbsUnits(line.Quantity)
		if err != nil || quantity == 0 || quantity > 2147483647 || !positiveSKU(line.Identity.SKU) {
			return nil, orders.ErrInvalid
		}
		if _, exists := units[line.Identity.Key()]; exists {
			return nil, orders.ErrAmbiguousLine
		}
		units[line.Identity.Key()] = quantity
	}
	return units, nil
}

func positiveSKU(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

func validatePackageConservation(posting orders.Posting, packages []orders.ActionPackage) error {
	remaining, err := currentPostingUnits(posting)
	if err != nil || len(packages) == 0 || len(packages) > 1000 {
		return orders.ErrInvalid
	}
	for _, box := range packages {
		if err = allocateActionLines(remaining, box.Lines); err != nil {
			return err
		}
	}
	if !fbsFullyAllocated(remaining) {
		return orders.ErrConflict
	}
	return nil
}

func allocateActionLines(remaining map[string]uint64, lines []orders.ActionQuantity) error {
	values := make([]FBSQuantity, 0, len(lines))
	seenSKU := make(map[string]bool, len(lines))
	for _, line := range lines {
		if !positiveSKU(line.Identity.SKU) || seenSKU[line.Identity.SKU] {
			return orders.ErrAmbiguousLine
		}
		seenSKU[line.Identity.SKU] = true
		values = append(values, FBSQuantity{Identity: line.Identity, Quantity: line.Quantity})
	}
	return fbsAllocateUnits(remaining, values)
}

func validatePartialQuantities(posting orders.Posting, input orders.ActionParameters) error {
	remaining, err := currentPostingUnits(posting)
	if err != nil || !executionText(input.ReasonMessage) {
		return orders.ErrInvalid
	}
	if err = allocateActionLines(remaining, input.Lines); err != nil {
		return err
	}
	if fbsFullyAllocated(remaining) {
		return orders.ErrConflict
	}
	return nil
}

func executionText(value string) bool {
	return len(strings.TrimSpace(value)) > 0 && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n")
}
