package orders

import "slices"

// WritesApplication changes the document's posting switch, which can activate
// or release aggregate reservations without changing native position fields.
func (command Command) WritesApplication() bool {
	return command.Action == ActionMirrorUpdate && command.CustomerOrder != nil && slices.Contains(command.CustomerOrder.OwnedFields, "applicable")
}

func (command Command) ValidateApplicationBaseline() error {
	baseline := command.PositionBaseline
	if baseline != nil && !ScalarPositionFrontier(command, *baseline, command.TargetPositions) {
		return ErrStale
	}
	if !command.WritesApplication() {
		return nil
	}
	if baseline == nil || baseline.Validate() != nil || baseline.Scope != command.Scope || baseline.Correlation != command.Correlation || baseline.RecoveryID != "" || baseline.EvidenceClass != "" && baseline.EvidenceClass != NativeDemandReadback {
		return ErrBlocked
	}
	if !command.CustomerOrder.Applicable.IsKnown() || !command.TargetPositionsComplete || len(command.TargetPositions) == 0 || len(baseline.Bindings) != len(command.TargetPositions) || len(command.CustomerOrder.Positions) != len(baseline.Bindings) {
		return ErrBlocked
	}
	for _, source := range command.CustomerOrder.Positions {
		binding, index, err := currentPositionBinding(command, *baseline, source)
		if err != nil {
			return err
		}
		current := command.TargetPositions[index]
		if !OwnedPositionFrontierMatches(binding.Native, current) || !PositionRepresented(source, current) || !current.Reserve.IsKnown() || !current.Shipped.IsKnown() {
			return ErrBlocked
		}
		if !nonnegativeNotGreater(*current.Reserve.Value, current.Quantity) || !ExactDecimalEqual(*current.Shipped.Value, "0") {
			return ErrBlocked
		}
		if !ExactDecimalEqual(*current.Reserve.Value, "0") && (!binding.Source.Reserve.IsKnown() || !desiredDecimal(binding.Source.Reserve, current.Reserve)) {
			return ErrBlocked
		}
	}
	return nil
}

func PlanApplicationBaseline(command Command, target TargetObservation, baseline PositionBaseline) (*PositionBaseline, error) {
	if !command.WritesApplication() {
		return nil, nil
	}
	if !target.Fields.Applicable.IsKnown() || !command.CustomerOrder.Applicable.IsKnown() {
		return nil, ErrBlocked
	}
	if *target.Fields.Applicable.Value == *command.CustomerOrder.Applicable.Value {
		return nil, ErrStale
	}
	if !target.Complete || target.Scope != command.Scope || target.Correlation != command.Correlation || target.Revision != command.ExpectedTargetRevision || !NativePositionsEqual(command.TargetPositions, target.Positions) {
		return nil, ErrStale
	}
	command.PositionBaseline = &baseline
	if err := command.ValidateApplicationBaseline(); err != nil {
		return nil, err
	}
	return &baseline, nil
}

func ApplicationTargetCurrent(command Command, target TargetObservation) error {
	if err := command.ValidateApplicationBaseline(); err != nil {
		return err
	}
	if !command.WritesApplication() {
		return nil
	}
	if !target.Complete || target.Scope != command.Scope || target.Correlation != command.Correlation || target.Revision != command.ExpectedTargetRevision || !NativePositionsEqual(command.TargetPositions, target.Positions) || !target.Fields.Applicable.IsKnown() {
		return ErrStale
	}
	if *target.Fields.Applicable.Value == *command.CustomerOrder.Applicable.Value {
		return ErrConflict
	}
	return nil
}

func ApplicationTargetObserved(command Command, target TargetObservation) bool {
	if command.ValidateApplicationBaseline() != nil {
		return false
	}
	if !command.WritesApplication() {
		return true
	}
	return target.Complete && target.Scope == command.Scope && target.Correlation == command.Correlation && NativePositionsEqual(command.TargetPositions, target.Positions) && nativeBoolEqual(command.CustomerOrder.Applicable, target.Fields.Applicable)
}

// ScalarPositionFrontier carries existing identities; matching values never
// authorize adopting a different native position after a scalar-only update.
func ScalarPositionFrontier(command Command, baseline PositionBaseline, actual []TargetPosition) bool {
	if command.Action != ActionMirrorUpdate || command.CustomerOrder == nil || baseline.Validate() != nil || baseline.Scope != command.Scope || baseline.Correlation != command.Correlation || !command.TargetPositionsComplete || len(command.CustomerOrder.Positions) != len(baseline.Bindings) {
		return false
	}
	for _, source := range command.CustomerOrder.Positions {
		binding, index, err := currentPositionBinding(command, baseline, source)
		if err != nil || !PositionRepresented(source, command.TargetPositions[index]) {
			return false
		}
		observed, found := nativePositionByID(actual, binding.Native.ID)
		if !found || !OwnedPositionFrontierMatches(binding.Native, observed) {
			return false
		}
	}
	return true
}

// Later knowledge of read-only counters is not a new inventory identity. Only
// originally missing read-only fields may remain unspecified in older proofs.
func OwnedPositionFrontierMatches(prior, current TargetPosition) bool {
	if prior.Shipped.IsMissing() {
		current.Shipped = prior.Shipped
	}
	if prior.Discount.IsMissing() {
		current.Discount = prior.Discount
	}
	if prior.VATEnabled.IsMissing() {
		current.VATEnabled = prior.VATEnabled
	}
	return NativePositionEqual(prior, current)
}

// LegacyScalarPositionBaseline compares an independent earlier ownership proof
// with an immutable later scalar readback. It never edits the historical command
// or treats the scalar update as creating the native positions.
func LegacyScalarPositionBaseline(work Work, target TargetObservation, prior PositionBaseline) (PositionBaseline, error) {
	if work.State != StateReconciled || work.Command.Action != ActionMirrorUpdate || work.Command.PositionBaseline != nil || !target.Complete || work.Scope != target.Scope {
		return PositionBaseline{}, ErrBlocked
	}
	comparison := work.Command
	comparison.TargetPositions = target.Positions
	comparison.TargetPositionsComplete = true
	if !ScalarPositionFrontier(comparison, prior, target.Positions) {
		return PositionBaseline{}, ErrStale
	}
	return prior, nil
}
