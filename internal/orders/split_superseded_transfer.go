package orders

import "encoding/json"

// NewSupersededSplitTransfer releases a child only after the positively
// superseded parent's entire owned reserve frontier was actually reconciled.
// It does not establish reserve attribution in an inventory stock snapshot.
func NewSupersededSplitTransfer(facts SplitReserveTransferFacts, retirement SplitSupersededReserveFacts) (SplitReserveTransferPermission, error) {
	if _, err := NewSupersededSplitReserve(retirement); err != nil {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	if !supersededTransferFactsMatch(facts, retirement) || !supersededTransferRelease(facts, retirement) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	proof := SplitReserveTransferPermission{Scope: facts.Before.Scope, SplitWorkID: facts.Split.ID, ParentWorkID: facts.ParentRelease.ID, PostingNumber: facts.Child.PostingNumber, ParentPosting: splitTransferSafePosting(facts.Parent), ChildPosting: splitTransferSafePosting(facts.Child), ParentCorrelation: facts.OriginalCorrelation, ParentTargetRevision: facts.ParentTarget.Revision, ParentStoreID: facts.ParentTarget.Fields.StoreID, ParentOrganizationID: facts.ParentTarget.Fields.OrganizationID, ParentPositions: facts.ParentTarget.Positions, ConfirmedAt: facts.ParentTarget.ObservedAt}
	if !proof.Allows(facts.Child) || !splitTransferChildReservable(facts.Child) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	// Freeze pointer-bearing source fields and native quantities. Future caller
	// mutations must not alter the sealed child or parent frontier.
	raw, err := json.Marshal(proof)
	if err != nil {
		return SplitReserveTransferPermission{}, ErrInvalid
	}
	var frozen SplitReserveTransferPermission
	if json.Unmarshal(raw, &frozen) != nil {
		return SplitReserveTransferPermission{}, ErrInvalid
	}
	return frozen, nil
}

func supersededTransferFactsMatch(facts SplitReserveTransferFacts, retirement SplitSupersededReserveFacts) bool {
	if !positionJSONEqual(minimalSupersededWork(facts.Split), minimalSupersededWork(retirement.Split)) || facts.OriginalCorrelation != retirement.OriginalCorrelation || !splitTransferPostingEqual(facts.Before, retirement.Before) || !splitTransferPostingEqual(facts.Parent, retirement.Parent) {
		return false
	}
	actual, current := facts.ParentTarget, retirement.Target
	if actual.Scope != current.Scope || actual.Correlation != current.Correlation || actual.Revision != current.Revision || actual.Complete != current.Complete || !actual.ObservedAt.Equal(current.ObservedAt) || !positionJSONEqual(actual.Fields, current.Fields) || !NativePositionsEqual(actual.Positions, current.Positions) || !positionJSONEqual(actual.SourceDeleted, current.SourceDeleted) {
		return false
	}
	for _, child := range retirement.Children {
		if child.PostingNumber == facts.Child.PostingNumber {
			return splitTransferPostingEqual(child, facts.Child)
		}
	}
	return false
}

func supersededTransferRelease(facts SplitReserveTransferFacts, retirement SplitSupersededReserveFacts) bool {
	work, target := facts.ParentRelease, facts.ParentTarget
	command := work.Command
	if command.Validate() != nil || work.Scope != facts.Before.Scope || !validID(work.ID) || work.State != StateReconciled || !CompositionDispatchAcknowledged(work) || command.Action != ActionMirrorPositionUpdate || command.Scope != work.Scope || command.Correlation != facts.OriginalCorrelation || command.SupersededSplitWorkID != facts.Split.ID || command.RetainedSplitWorkID != "" || command.SplitTransfer != nil {
		return false
	}
	if command.PositionPatch == nil || command.CustomerOrder == nil || command.PostingNumber != facts.Parent.PostingNumber || command.CommercialOrderID != facts.Parent.CommercialOrderID || command.ExpectedSourceRevision != facts.Parent.Evidence.Revision || !compositionReferencesMatch(command, target) || !PositionPatchObserved(command, target) {
		return false
	}
	if !supersededTransferWarehouse(command.Warehouse, facts.Split.Command.Warehouse) || !supersededTransferOldPlan(command, retirement) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(work, target)
	return err == nil && baseline.Equal(retirement.Baseline) && supersededTransferAllReleased(baseline)
}

func supersededTransferOldPlan(command Command, retirement SplitSupersededReserveFacts) bool {
	// This reconstructs only the recorded command's preimage. It is never a
	// provider observation or a substitute for the current target read above.
	before := retirement
	before.Baseline = command.PositionPatch.Baseline
	before.Target = TargetObservation{Scope: command.Scope, Correlation: command.Correlation, Revision: command.ExpectedTargetRevision, Complete: command.TargetPositionsComplete, Positions: command.TargetPositions, Fields: *command.CustomerOrder, ObservedAt: before.Baseline.ObservedAt}
	permission, err := NewSupersededSplitReserve(before)
	if err != nil {
		return false
	}
	plan, err := PlanSupersededSplitReserve(command, permission)
	return err == nil && plan.PositionPatch != nil && positionJSONEqual(plan.PositionPatch, command.PositionPatch) && positionJSONEqual(plan.CustomerOrder, *command.CustomerOrder)
}

func supersededTransferAllReleased(baseline PositionBaseline) bool {
	if baseline.Validate() != nil || baseline.RecoveryID != "" || baseline.EvidenceClass == "OPERATOR_ATTESTED" || len(baseline.Bindings) == 0 {
		return false
	}
	for _, binding := range baseline.Bindings {
		if !binding.Source.Reserve.IsKnown() || !decimalZero(*binding.Source.Reserve.Value) || !binding.Native.Reserve.IsKnown() || !decimalZero(*binding.Native.Reserve.Value) || !binding.Native.Shipped.IsKnown() || !decimalZero(*binding.Native.Shipped.Value) {
			return false
		}
	}
	return true
}

func supersededTransferWarehouse(actual, expected WarehouseMappingRef) bool {
	return actual.ID == expected.ID && actual.Version == expected.Version && actual.Scope == expected.Scope && actual.OzonWarehouseID == expected.OzonWarehouseID && actual.MoyskladStoreID == expected.MoyskladStoreID
}
