package orderinventory

import "github.com/lilter96/styk/internal/orders"

// Superseded is a separate, positive split fact, not an ordinary cancellation.
// The constructor-supplied permission proves owned reserve release; fresh native
// reads and a later real stock traversal still establish baseline inclusion.
func assessSupersededParent(input Input, observation Observation, result Obligation) Obligation {
	proof, work, target := observation.SplitSuperseded, observation.Work, observation.Target
	if proof == nil || work == nil || target == nil || !knownStatus(observation.Posting, orders.SupersededSplitParentStatus) || !nativePointBaselineOrdered(input, observation) {
		return pending(result, Unreflected, "superseded_parent_release_unproven")
	}
	if observation.NativePointTargetRevision == "" || observation.NativePointTargetRevision != proof.ParentTargetRevision || proof.Scope != observation.Posting.Scope || proof.ParentWorkID != work.ID || proof.SplitWorkID != work.Command.SupersededSplitWorkID || proof.ParentCorrelation != observation.Binding || proof.ParentStoreID != input.Pool.WarehouseID || !proof.ParentSourceMatches(observation.Posting) || target.Fields.Applicable.Validate() != nil || !proof.ParentTargetMatches(*target) || !proof.ConfirmedAt.Equal(target.ObservedAt) {
		return pending(result, Unreflected, "superseded_parent_frontier_changed")
	}
	patch := work.Command.PositionPatch
	if work.Command.Action != orders.ActionMirrorPositionUpdate || !orders.CompositionDispatchAcknowledged(*work) || patch == nil || patch.Quantity.IsSpecified() || patch.Price.IsSpecified() || patch.Reserve.Validate() != nil || !patch.Reserve.IsKnown() || !orders.ExactDecimalEqual(*patch.Reserve.Value, "0") || !orders.PositionPatchObserved(work.Command, *target) {
		return pending(result, Unreflected, "superseded_owned_release_not_reconciled")
	}
	baseline, err := orders.ProjectionPositionBaseline(*work, *target)
	if err != nil || baseline.RecoveryID != "" || baseline.EvidenceClass == "OPERATOR_ATTESTED" || !supersededOwnedReserveZero(baseline) {
		return pending(result, Unreflected, "superseded_owned_reserve_not_zero")
	}
	// Keep the actual total reserve, including unowned same-pool positions. The
	// commitment calculator already deducts the aggregate source reserve once.
	result.State = Superseded
	return result
}

func supersededOwnedReserveZero(baseline orders.PositionBaseline) bool {
	if baseline.Validate() != nil {
		return false
	}
	for _, binding := range baseline.Bindings {
		for _, field := range []orders.Field[orders.Decimal]{binding.Source.Reserve, binding.Native.Reserve, binding.Native.Shipped} {
			if field.Validate() != nil || !field.IsKnown() || !orders.ExactDecimalEqual(*field.Value, "0") {
				return false
			}
		}
	}
	return true
}
