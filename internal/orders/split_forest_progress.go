package orders

// A reconciled scalar patch may have applied only one of several pending row
// reductions. Replay its actual UUID frontier, never the full desired command.
// Each event must move strictly toward the immutable split allocation without
// increasing any owned quantity or reserve. Admission still needs all rows.
func stagedSplitReductionProgress(work Work, target TargetObservation, before PositionBaseline) bool {
	after, err := ProjectionPositionBaseline(work, target)
	return err == nil && stagedSplitFrontier(work.Command, before, after)
}

func stagedSplitFrontier(command Command, before, after PositionBaseline) bool {
	if command.CustomerOrder == nil || before.Validate() != nil || after.Validate() != nil || before.Scope != after.Scope || before.Correlation != after.Correlation || len(after.Bindings) > len(before.Bindings) {
		return false
	}
	changed := len(after.Bindings) < len(before.Bindings)
	for _, prior := range before.Bindings {
		next, exists := replacementBinding(after, prior.Source.Identity)
		wanted, retained := stagedSplitWanted(*command.CustomerOrder, prior.Source.Identity)
		if !exists {
			if retained || command.PositionReplacement == nil || !retainedSplitBindingUnshipped(prior) {
				return false
			}
			continue
		}
		if !retained || !stagedSplitBinding(prior, next, wanted) {
			return false
		}
		changed = changed || !ExactDecimalEqual(prior.Native.Quantity, next.Native.Quantity) || !SameObservedDecimal(prior.Native.Reserve, next.Native.Reserve)
	}
	return changed
}

func stagedSplitWanted(write CustomerOrderWrite, identity LineIdentity) (PositionWrite, bool) {
	for _, wanted := range write.Positions {
		if wanted.Identity.Key() == identity.Key() {
			return wanted, true
		}
	}
	return PositionWrite{}, false
}

func stagedSplitBinding(before, after PositionBinding, wanted PositionWrite) bool {
	if !positionMappingsEqual(before.Source.Mapping, after.Source.Mapping) || !positionMappingsEqual(wanted.Mapping, after.Source.Mapping) || !retainedSplitBindingUnshipped(before) || !retainedSplitBindingUnshipped(after) || !wanted.Reserve.IsKnown() {
		return false
	}
	if !nonnegativeNotGreater(after.Native.Quantity, before.Native.Quantity) || !nonnegativeNotGreater(wanted.Quantity, after.Native.Quantity) || !nonnegativeNotGreater(*after.Native.Reserve.Value, *before.Native.Reserve.Value) || !nonnegativeNotGreater(*wanted.Reserve.Value, *after.Native.Reserve.Value) {
		return false
	}
	// NativePositionEqual also retains UUID, assortment, price, discount and VAT.
	// Only quantity and reserve may differ in this split settlement history.
	expected := before.Native
	expected.Quantity, expected.Reserve = after.Native.Quantity, after.Native.Reserve
	return OwnedPositionFrontierMatches(expected, after.Native)
}
