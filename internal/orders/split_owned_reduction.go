package orders

// OwnedSplitReductionBaseline accepts either one sealed owned-position patch or
// a manually reviewed collection replacement. Callers must still prove the
// actual split and compare this baseline to immutable native history.
func OwnedSplitReductionBaseline(command Command) (PositionBaseline, error) {
	if command.PositionPatch != nil && command.PositionReplacement == nil && command.Action == ActionMirrorPositionUpdate {
		if command.PositionPatch.Validate(command) != nil || !retainedSplitWriteReduced(*command.CustomerOrder, command.PositionPatch.Baseline) {
			return PositionBaseline{}, ErrBlocked
		}
		return command.PositionPatch.Baseline, nil
	}
	if command.PositionReplacement != nil && command.PositionPatch == nil && command.Action == ActionMirrorCompositionUpdate && command.PositionReplacement.Validate(command) == nil && len(command.PositionReplacement.Added) == 0 && retainedSplitWriteReduced(*command.CustomerOrder, command.PositionReplacement.Baseline) {
		return command.PositionReplacement.Baseline, nil
	}
	return PositionBaseline{}, ErrBlocked
}

func OwnedSplitReductionObserved(command Command, target TargetObservation) bool {
	if _, err := OwnedSplitReductionBaseline(command); err != nil {
		return false
	}
	if command.PositionReplacement != nil {
		return CompositionTargetObserved(command, target)
	}
	return PositionPatchObserved(command, target)
}
