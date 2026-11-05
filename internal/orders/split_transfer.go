package orders

import (
	"slices"
	"time"
)

// SplitReserveTransferFacts contains existing immutable operations and actual
// observations. The store separately proves no earlier local split ancestry,
// current authority, fresh provider facts and group-level dispatch exclusion.
type SplitReserveTransferFacts struct {
	Split               Work
	Before              Posting
	OriginalCorrelation Correlation
	ParentRelease       Work
	Parent              Posting
	ParentTarget        TargetObservation
	Child               Posting
}

// SplitReserveTransferPermission records a verified retained-parent reduction.
// It does not establish that native reserves occur in a stock snapshot; that
// remains a separate commitment-engine attribution decision.
type SplitReserveTransferPermission struct {
	Scope                Scope            `json:"scope"`
	SplitWorkID          string           `json:"split_work_id"`
	ParentWorkID         string           `json:"parent_work_id"`
	PostingNumber        string           `json:"posting_number"`
	ParentPosting        Posting          `json:"parent_posting"`
	ChildPosting         Posting          `json:"child_posting"`
	ParentCorrelation    Correlation      `json:"parent_correlation"`
	ParentStoreID        string           `json:"parent_store_id"`
	ParentOrganizationID string           `json:"parent_organization_id"`
	ParentTargetRevision string           `json:"parent_target_revision"`
	ParentPositions      []TargetPosition `json:"parent_positions"`
	ConfirmedAt          time.Time        `json:"confirmed_at"`
}

func NewSplitReserveTransfer(facts SplitReserveTransferFacts) (SplitReserveTransferPermission, error) {
	if !splitTransferRootVerified(facts) || !splitTransferAllocationVerified(facts) || !splitTransferParentVerified(facts) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	proof := SplitReserveTransferPermission{Scope: facts.Before.Scope, SplitWorkID: facts.Split.ID, ParentWorkID: facts.ParentRelease.ID, PostingNumber: facts.Child.PostingNumber, ParentPosting: splitTransferSafePosting(facts.Parent), ChildPosting: splitTransferSafePosting(facts.Child), ParentCorrelation: facts.OriginalCorrelation, ParentTargetRevision: facts.ParentTarget.Revision, ParentStoreID: facts.ParentTarget.Fields.StoreID, ParentOrganizationID: facts.ParentTarget.Fields.OrganizationID, ParentPositions: slices.Clone(facts.ParentTarget.Positions), ConfirmedAt: facts.ParentTarget.ObservedAt}
	if !proof.Allows(facts.Child) || !splitTransferChildReservable(facts.Child) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	return proof, nil
}

func splitTransferRootVerified(facts SplitReserveTransferFacts) bool {
	split, before := facts.Split, facts.Before
	if before.Validate() != nil || before.ParentPostingNumber.IsKnown() || len(before.ChildPostingNumbers) > 0 || split.Scope != before.Scope || !validID(split.ID) || split.State != StateReconciled || split.DispatchCount != 1 {
		return false
	}
	command := split.Command
	if !command.Action.IsSplit() || command.PostingAction == nil || command.Scope != before.Scope || command.PostingNumber != before.PostingNumber || command.CommercialOrderID != before.CommercialOrderID || command.ExpectedSourceRevision != before.Evidence.Revision {
		return false
	}
	if command.Action == ActionPostingSplitUnassembled && !unassembledAcknowledgedAllocation(split) {
		return false
	}
	for _, member := range split.Receipt.ObservedPostings {
		if !splitTransferProductIdentities(before.Lines, member.Lines) {
			return false
		}
	}
	original := facts.OriginalCorrelation
	return original.Validate() == nil && original.Scope == before.Scope && original.PostingNumber == before.PostingNumber && original.CustomerOrderID != "" && command.Correlation == original && split.Receipt.CommandID == command.ID && split.Receipt.State == StateReconciled && split.Receipt.HTTPStatus >= 200 && split.Receipt.HTTPStatus < 300
}

func splitTransferAllocationVerified(facts SplitReserveTransferFacts) bool {
	receipt, before := facts.Split.Receipt, facts.Before
	if !SplitObservedForAction(facts.Split.Command.Action, before, facts.Split.Command.PostingAction.Parameters.Packages, receipt.PostingNumbers, receipt.ObservedPostings) || facts.Parent.PostingNumber != before.PostingNumber || facts.Child.PostingNumber == before.PostingNumber {
		return false
	}
	retained, child := false, false
	for _, observed := range receipt.ObservedPostings {
		if observed.PostingNumber == facts.Parent.PostingNumber {
			retained = splitTransferSameAllocation(observed, facts.Parent)
		}
		if observed.PostingNumber == facts.Child.PostingNumber {
			child = splitTransferSameAllocation(observed, facts.Child)
		}
	}
	return retained && child && len(facts.Child.ChildPostingNumbers) == 0 && (!facts.Child.ParentPostingNumber.IsKnown() || *facts.Child.ParentPostingNumber.Value == before.PostingNumber)
}

func splitTransferSameAllocation(before, current Posting) bool {
	if current.Validate() != nil || !sameFulfillmentTarget(before, current) {
		return false
	}
	wanted, invalid := reservationLineQuantities(before.Lines)
	if invalid.Code != "" || len(wanted) != len(current.Lines) {
		return false
	}
	actual, invalid := reservationLineQuantities(current.Lines)
	if invalid.Code != "" {
		return false
	}
	for key, quantity := range wanted {
		if !ExactDecimalEqual(quantity, actual[key]) {
			return false
		}
	}
	return splitTransferProductIdentities(before.Lines, current.Lines)
}

func splitTransferParentVerified(facts SplitReserveTransferFacts) bool {
	work, target := facts.ParentRelease, facts.ParentTarget
	command := work.Command
	prior, err := OwnedSplitReductionBaseline(command)
	if command.Validate() != nil || err != nil {
		return false
	}
	if work.Scope != facts.Before.Scope || !validID(work.ID) || work.State != StateReconciled || command.Scope != work.Scope || command.Correlation != facts.OriginalCorrelation || command.RetainedSplitWorkID != facts.Split.ID || !CompositionDispatchAcknowledged(work) {
		return false
	}
	if command.CustomerOrder == nil || command.CommercialOrderID != facts.Before.CommercialOrderID || command.ExpectedSourceRevision != facts.Parent.Evidence.Revision || command.PostingNumber != facts.Parent.PostingNumber {
		return false
	}
	if target.Scope != work.Scope || target.Correlation != facts.OriginalCorrelation || !validID(target.Revision) || !target.Complete || target.ObservedAt.IsZero() || !target.Fields.Applicable.IsKnown() || !*target.Fields.Applicable.Value || !compositionReferencesMatch(command, target) || !OwnedSplitReductionObserved(command, target) {
		return false
	}
	retained := SplitRetainedReservePermission{Scope: work.Scope, SplitWorkID: facts.Split.ID, Before: facts.Before, Correlation: facts.OriginalCorrelation}
	if retained.ValidateWrite(facts.Parent, *command.CustomerOrder, prior) != nil {
		return false
	}
	baseline, err := ProjectionPositionBaseline(work, target)
	return err == nil && splitTransferAllParentLinesReserved(facts.Parent, baseline)
}

func splitTransferAllParentLinesReserved(parent Posting, baseline PositionBaseline) bool {
	wanted, invalid := reservationLineQuantities(parent.Lines)
	if invalid.Code != "" || len(wanted) != len(baseline.Bindings) || !splitTransferCancellationKnown(parent) {
		return false
	}
	expected := CustomerOrderWrite{Applicable: Present(true), Positions: make([]PositionWrite, 0, len(baseline.Bindings))}
	for _, binding := range baseline.Bindings {
		expected.Positions = append(expected.Positions, binding.Source)
	}
	if invalid = applyOpenQuantityReserve(parent, &expected, baseline, true); invalid.Code != "" {
		return false
	}
	for i, binding := range baseline.Bindings {
		quantity, found := wanted[binding.Source.Identity.Key()]
		if !found || !ExactDecimalEqual(quantity, binding.Source.Quantity) || !SameObservedDecimal(expected.Positions[i].Reserve, binding.Source.Reserve) || !SameObservedDecimal(expected.Positions[i].Reserve, binding.Native.Reserve) || !binding.Native.Shipped.IsKnown() || !decimalZero(*binding.Native.Shipped.Value) {
			return false
		}
		delete(wanted, binding.Source.Identity.Key())
	}
	return len(wanted) == 0
}

func splitTransferCancellationKnown(parent Posting) bool {
	if !parent.Status.IsKnown() || *parent.Status.Value != "cancelled" {
		return true
	}
	return parent.Cancellation.IsKnown() && parent.Cancellation.Value.AfterShipment.IsKnown() && !*parent.Cancellation.Value.AfterShipment.Value
}

func (permission SplitReserveTransferPermission) Allows(posting Posting) bool {
	if !validID(permission.ParentStoreID) || !validID(permission.ParentOrganizationID) || permission.Scope.Validate() != nil || permission.Scope != posting.Scope || !validID(permission.SplitWorkID) || !validID(permission.ParentWorkID) || permission.ParentWorkID == permission.SplitWorkID || permission.PostingNumber != posting.PostingNumber || permission.ParentPosting.PostingNumber == posting.PostingNumber || permission.ConfirmedAt.IsZero() || !validID(permission.ParentTargetRevision) || len(permission.ParentPositions) == 0 {
		return false
	}
	if permission.ParentPosting.Validate() != nil || permission.ParentPosting.Scope != permission.Scope || permission.ParentCorrelation.Validate() != nil || permission.ParentCorrelation.Scope != permission.Scope || permission.ParentCorrelation.PostingNumber != permission.ParentPosting.PostingNumber || permission.ParentCorrelation.CustomerOrderID == "" || len(posting.ChildPostingNumbers) > 0 {
		return false
	}
	return splitTransferPostingEqual(permission.ChildPosting, posting)
}

func ApplyTransferredSplitOpenQuantityReserve(posting Posting, write *CustomerOrderWrite, baseline PositionBaseline, permission SplitReserveTransferPermission) Validation {
	if write == nil || !permission.Allows(posting) || write.StoreID != permission.ParentStoreID || write.OrganizationID != permission.ParentOrganizationID {
		return reservePolicyValidation("posting.lineage", "reservation_policy_split_transfer_proof_required")
	}
	return applyOpenQuantityReserve(posting, write, baseline, true)
}

func (permission SplitReserveTransferPermission) Equal(other SplitReserveTransferPermission) bool {
	return permission.Scope == other.Scope && permission.SplitWorkID == other.SplitWorkID && permission.ParentWorkID == other.ParentWorkID && permission.PostingNumber == other.PostingNumber && permission.ParentCorrelation == other.ParentCorrelation && permission.ParentStoreID == other.ParentStoreID && permission.ParentOrganizationID == other.ParentOrganizationID && permission.ParentTargetRevision == other.ParentTargetRevision && permission.ConfirmedAt.Equal(other.ConfirmedAt) && NativePositionsEqual(permission.ParentPositions, other.ParentPositions) && splitTransferPostingEqual(permission.ParentPosting, other.ParentPosting) && splitTransferPostingEqual(permission.ChildPosting, other.ChildPosting)
}

func splitTransferPostingEqual(before, after Posting) bool {
	if before.Validate() != nil || after.Validate() != nil {
		return false
	}
	before, after = splitTransferAllocationPosting(before), splitTransferAllocationPosting(after)
	left, err := PostingFingerprint(before)
	if err != nil {
		return false
	}
	right, err := PostingFingerprint(after)
	return err == nil && left == right && before.Evidence.Revision == after.Evidence.Revision
}

func splitTransferSafePosting(posting Posting) Posting {
	posting.PII, posting.Customer = nil, ProtectedCustomer{}
	posting.RelatedPostings = posting.RelatedPostings.Canonical()
	posting.Lines = slices.Clone(posting.Lines)
	posting.ChildPostingNumbers = slices.Clone(posting.ChildPostingNumbers)
	return posting
}

func splitTransferChildReservable(posting Posting) bool {
	write := CustomerOrderWrite{Applicable: Present(true), Positions: make([]PositionWrite, 0, len(posting.Lines))}
	for _, line := range posting.Lines {
		write.Positions = append(write.Positions, PositionWrite{Identity: line.Identity, Quantity: line.Quantity})
	}
	return applyOpenQuantityReserve(posting, &write, PositionBaseline{}, true).Code == ""
}

// AllowsCommand validates a sealed child write without granting execution or
// filling missing quantities. Current authority and native guards still apply.
func (permission SplitReserveTransferPermission) AllowsCommand(command Command) bool {
	if !command.Action.IsMirror() || command.Scope != permission.Scope || command.PostingNumber != permission.PostingNumber || command.CustomerOrder == nil || command.CommercialOrderID != permission.ChildPosting.CommercialOrderID || command.ExpectedSourceRevision != permission.ChildPosting.Evidence.Revision || command.Correlation.CustomerOrderID == permission.ParentCorrelation.CustomerOrderID {
		return false
	}
	candidate := *command.CustomerOrder
	candidate.Positions = slices.Clone(candidate.Positions)
	if invalid := ApplyTransferredSplitOpenQuantityReserve(permission.ChildPosting, &candidate, PositionBaseline{}, permission); invalid.Code != "" {
		return false
	}
	for i, wanted := range command.CustomerOrder.Positions {
		if !SameObservedDecimal(wanted.Reserve, candidate.Positions[i].Reserve) {
			return false
		}
	}
	return true
}

func splitTransferProductIdentities(before, after []PostingLine) bool {
	previous := make(map[string]Field[string], len(before))
	for _, line := range before {
		previous[line.Identity.Key()] = line.ProductID
	}
	for _, line := range after {
		original, found := previous[line.Identity.Key()]
		if !found || original.IsKnown() && (!line.ProductID.IsKnown() || *original.Value != *line.ProductID.Value) {
			return false
		}
	}
	return true
}

// Compare the same minimal allocation on both sides: SQL proof reads omit
// unmanaged customer, delivery and financial metadata. Those blocks and display
// names cannot establish reserve ownership. Identity, quantities, line money,
// status, cancellation, source versions and drift remain checked.
func splitTransferAllocationPosting(posting Posting) Posting {
	result := minimalSupersededPosting(posting)
	result.RelatedPostings = nil
	return result
}
