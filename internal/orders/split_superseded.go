package orders

import (
	"reflect"
	"slices"
)

const SupersededSplitParentStatus = "cancelled_from_split_pending"

// These facts are reconstructed from journaled work and positive provider
// details. A 404, a related-postings list or absent parent is never authority.
type SplitSupersededReserveFacts struct {
	Split               Work
	Before              Posting
	OriginalCorrelation Correlation
	Parent              Posting
	Children            []Posting
	Baseline            PositionBaseline
	Target              TargetObservation
}

// This authorizes releasing owned native reserves; it neither retires a stock
// obligation nor claims that every native position was already released.
type SplitSupersededReservePermission struct {
	// Transient constructor witness. Persist only the split work identity and
	// frozen command frontier; rebuild this permission from scoped facts at use.
	witness              *SplitSupersededReserveFacts
	Scope                Scope            `json:"scope"`
	SplitWorkID          string           `json:"split_work_id"`
	Before               Posting          `json:"before"`
	ParentPosting        Posting          `json:"parent_posting"`
	ChildPostings        []Posting        `json:"child_postings"`
	ParentCorrelation    Correlation      `json:"parent_correlation"`
	Baseline             PositionBaseline `json:"baseline"`
	ParentTargetRevision string           `json:"parent_target_revision"`
	ParentPositions      []TargetPosition `json:"parent_positions"`
	ParentStoreID        string           `json:"parent_store_id"`
	ParentOrganizationID string           `json:"parent_organization_id"`
}

// A nil PositionPatch means no external mutation is required, never permission
// to fall back to a scalar or collection write.
type SplitSupersededReservePlan struct {
	CustomerOrder CustomerOrderWrite
	PositionPatch *PositionPatch
}

func NewSupersededSplitReserve(facts SplitSupersededReserveFacts) (SplitSupersededReservePermission, error) {
	if !validSupersededSplitFacts(facts) {
		return SplitSupersededReservePermission{}, ErrBlocked
	}
	witness, err := freezeSupersededFacts(facts)
	if err != nil {
		return SplitSupersededReservePermission{}, err
	}
	public, err := freezeSupersededFacts(witness)
	if err != nil {
		return SplitSupersededReservePermission{}, err
	}
	permission := supersededPermissionValues(public)
	permission.witness = &witness
	return permission, nil
}

func validSupersededSplitFacts(facts SplitSupersededReserveFacts) bool {
	if !supersededSourcesTrustworthy(facts) || facts.Split.Command.Warehouse.Validate() != nil || facts.Split.Command.Warehouse.Scope != facts.Before.Scope || !facts.Before.WarehouseID.IsKnown() || facts.Split.Command.Warehouse.OzonWarehouseID != *facts.Before.WarehouseID.Value {
		return false
	}
	transfer := SplitReserveTransferFacts{Split: facts.Split, Before: facts.Before, OriginalCorrelation: facts.OriginalCorrelation}
	return splitTransferRootVerified(transfer) && supersededSplitSource(facts) && supersededSplitBaseline(facts)
}

// Validate is not authorization of serialized caller data. A real constructor
// must have witnessed scoped immutable work and current provider observations.
// Database and dispatch guards independently reconstruct those facts at use.
func (permission SplitSupersededReservePermission) Validate() error {
	if permission.witness == nil || !validSupersededSplitFacts(*permission.witness) {
		return ErrBlocked
	}
	expected := supersededPermissionValues(*permission.witness)
	permission.witness = nil
	if !reflect.DeepEqual(permission, expected) {
		return ErrStale
	}
	return nil
}

func supersededSplitSource(facts SplitSupersededReserveFacts) bool {
	before, parent, receipt := facts.Before, facts.Parent, facts.Split.Receipt
	if parent.Validate() != nil || !splitTransferSameAllocation(before, parent) || !sameFulfillmentTarget(before, parent) || !parent.Status.IsKnown() || *parent.Status.Value != SupersededSplitParentStatus || parent.ParentPostingNumber.IsKnown() {
		return false
	}
	if slices.Contains(receipt.PostingNumbers, before.PostingNumber) || !SplitObservedForAction(facts.Split.Command.Action, before, facts.Split.Command.PostingAction.Parameters.Packages, receipt.PostingNumbers, receipt.ObservedPostings) || !SplitObservedForAction(facts.Split.Command.Action, before, facts.Split.Command.PostingAction.Parameters.Packages, receipt.PostingNumbers, facts.Children) {
		return false
	}
	if len(parent.ChildPostingNumbers) > 0 && !sameSupersededNumbers(parent.ChildPostingNumbers, receipt.PostingNumbers) {
		return false
	}
	if parent.Times.DeliveringAt.IsKnown() || parent.Times.DeliveredAt.IsKnown() || reservationAfterShipment(parent.Cancellation) {
		return false
	}
	previous := make(map[string]Posting, len(receipt.ObservedPostings))
	for _, child := range receipt.ObservedPostings {
		previous[child.PostingNumber] = child
	}
	for _, child := range facts.Children {
		if child.PostingNumber == before.PostingNumber || len(child.ChildPostingNumbers) > 0 || child.ParentPostingNumber.IsKnown() && *child.ParentPostingNumber.Value != before.PostingNumber || !splitTransferSameAllocation(previous[child.PostingNumber], child) {
			return false
		}
	}
	return true
}

func supersededSplitBaseline(facts SplitSupersededReserveFacts) bool {
	baseline, target := facts.Baseline, facts.Target
	if target.Fields.Applicable.Validate() != nil || !supersededNativeFrontierValid(target.Positions) || target.SourceDeleted.Validate() != nil || target.SourceDeleted.IsKnown() && *target.SourceDeleted.Value != "" {
		return false
	}
	if baseline.Validate() != nil || baseline.Scope != facts.Before.Scope || baseline.Correlation != facts.OriginalCorrelation || baseline.RecoveryID != "" || baseline.EvidenceClass == "OPERATOR_ATTESTED" {
		return false
	}
	if target.Scope != baseline.Scope || target.Correlation != baseline.Correlation || !target.Complete || !validID(target.Revision) || target.ObservedAt.IsZero() || !target.Fields.Applicable.IsKnown() || !*target.Fields.Applicable.Value || !validID(target.Fields.OrganizationID) || target.Fields.StoreID != facts.Split.Command.Warehouse.MoyskladStoreID || !validID(target.Fields.StoreID) {
		return false
	}
	if !supersededSourceBindings(facts.Before, baseline) {
		return false
	}
	command := Command{Scope: baseline.Scope, Correlation: baseline.Correlation, CustomerOrder: &CustomerOrderWrite{Positions: supersededZeroReserveSources(baseline)}, TargetPositions: target.Positions, TargetPositionsComplete: true}
	_, err := PlanPositionPatch(command, baseline)
	return err == nil
}

func supersededSourceBindings(before Posting, baseline PositionBaseline) bool {
	original, invalid := reservationLineQuantities(before.Lines)
	if invalid.Code != "" || len(original) != len(baseline.Bindings) {
		return false
	}
	for _, binding := range baseline.Bindings {
		quantity, found := original[binding.Source.Identity.Key()]
		if !found || !ExactDecimalEqual(quantity, binding.Source.Quantity) || !binding.Source.Reserve.IsKnown() || !binding.Native.Reserve.IsKnown() || !nonnegativeNotGreater(*binding.Source.Reserve.Value, binding.Source.Quantity) || !nonnegativeNotGreater(*binding.Native.Reserve.Value, binding.Native.Quantity) || !binding.Native.Shipped.IsKnown() || !decimalZero(*binding.Native.Shipped.Value) {
			return false
		}
		if !supersededBeforeValues(before, binding.Source) {
			return false
		}
		delete(original, binding.Source.Identity.Key())
	}
	return len(original) == 0
}

func supersededBeforeValues(before Posting, source PositionWrite) bool {
	for _, line := range before.Lines {
		if line.Identity.Key() != source.Identity.Key() {
			continue
		}
		return (!line.ProductID.IsKnown() || *line.ProductID.Value == source.Mapping.OzonProductID) && desiredMoney(line.Price, source.Price) && desiredDecimal(line.VAT, source.VAT)
	}
	return false
}

func supersededZeroReserveSources(baseline PositionBaseline) []PositionWrite {
	result := make([]PositionWrite, 0, len(baseline.Bindings))
	for _, binding := range baseline.Bindings {
		source := binding.Source
		source.Reserve = Present(Decimal("0"))
		result = append(result, source)
	}
	return result
}

func PlanSupersededSplitReserve(command Command, permission SplitSupersededReservePermission) (SplitSupersededReservePlan, error) {
	if permission.Validate() != nil || !permission.commandTargetMatches(command) {
		return SplitSupersededReservePlan{}, ErrStale
	}
	write := *command.CustomerOrder
	write.Positions = supersededZeroReserveSources(permission.Baseline)
	candidate := command
	candidate.CustomerOrder = &write
	patch, err := PlanPositionPatch(candidate, permission.Baseline)
	if err != nil {
		return SplitSupersededReservePlan{}, err
	}
	if patch != nil {
		if patch.Quantity.IsSpecified() || patch.Price.IsSpecified() || !patch.Reserve.IsKnown() || !decimalZero(*patch.Reserve.Value) {
			return SplitSupersededReservePlan{}, ErrBlocked
		}
		write.Positions, write.OwnedFields = patch.appliedSources(), patch.Fields()
	} else {
		write.OwnedFields = nil
	}
	return SplitSupersededReservePlan{CustomerOrder: write, PositionPatch: patch}, nil
}

func (permission SplitSupersededReservePermission) commandTargetMatches(command Command) bool {
	parent := permission.ParentPosting
	if command.Action != ActionMirrorUpdate && command.Action != ActionMirrorPositionUpdate {
		return false
	}
	if command.CustomerOrder == nil || command.Scope != permission.Scope || command.Correlation != permission.ParentCorrelation || command.PostingNumber != parent.PostingNumber || command.CommercialOrderID != parent.CommercialOrderID || command.ExpectedSourceRevision != parent.Evidence.Revision || command.ExpectedTargetRevision != permission.ParentTargetRevision || !command.TargetPositionsComplete || !NativePositionsEqual(command.TargetPositions, permission.ParentPositions) {
		return false
	}
	write := command.CustomerOrder
	if write.Applicable.Validate() != nil {
		return false
	}
	if write.StoreID != permission.ParentStoreID || write.OrganizationID != permission.ParentOrganizationID || !write.Applicable.IsKnown() || !*write.Applicable.Value || len(write.Positions) != len(permission.Baseline.Bindings) {
		return false
	}
	seen := make(map[string]bool, len(write.Positions))
	for _, wanted := range write.Positions {
		if wanted.Validate(command.Scope) != nil || seen[wanted.Identity.Key()] {
			return false
		}
		seen[wanted.Identity.Key()] = true
		found := false
		for _, binding := range permission.Baseline.Bindings {
			if wanted.Identity.Key() == binding.Source.Identity.Key() {
				found = positionMappingsEqual(wanted.Mapping, binding.Source.Mapping) && ExactDecimalEqual(wanted.Quantity, binding.Source.Quantity) && desiredMoney(wanted.Price, binding.Source.Price) && desiredDecimal(wanted.VAT, binding.Source.VAT)
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameSupersededNumbers(a, b []string) bool {
	left, right := slices.Clone(a), slices.Clone(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
