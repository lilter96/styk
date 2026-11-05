package orders

// NativeUnpostedParentEffect describes an explicitly selected provider profile,
// not a fabricated shipment counter. A linked applied document can leave every
// counter of an unposted customerorder unchanged. Document matching, money,
// durable consumption and fresh stock inclusion remain separate requirements.
func NativeUnpostedParentEffect(command Command, target TargetObservation) bool {
	m := command.NativeMovement
	return m != nil && m.ParentEffect == NativeParentUnpostedDocument && nativeFalse(m.ParentApplicable) && nativeFalse(target.Fields.Applicable) && nativeUnpostedSelected(command) && NativePositionsEqual(command.TargetPositions, target.Positions)
}

// NativeUnpostedParentSelection checks the frozen zero-counter profile only;
// it does not prove a provider read or authorize a physical movement.
func NativeUnpostedParentSelection(command Command) bool {
	return command.NativeMovement != nil && command.NativeMovement.ParentEffect == NativeParentUnpostedDocument && nativeUnpostedSelected(command)
}

func nativeFalse(value Field[bool]) bool {
	return value.Validate() == nil && value.IsKnown() && !*value.Value
}

func nativeUnpostedSelected(command Command) bool {
	m := command.NativeMovement
	if m == nil || !m.Applicable.IsKnown() || !*m.Applicable.Value || !nativeFalse(m.ParentApplicable) {
		return false
	}
	for _, line := range m.Lines {
		current, found := nativePositionByID(command.TargetPositions, line.OrderPositionID)
		if !found || !nativeUnpostedPosition(current) {
			return false
		}
		bound := false
		for _, binding := range m.SourceBaseline.Bindings {
			if binding.Native.ID == current.ID && binding.Source.Identity == line.Identity && positionMappingsEqual(binding.Source.Mapping, line.Mapping) && nativeUnpostedPosition(binding.Native) && binding.Source.Reserve.IsKnown() && ExactDecimalEqual(*binding.Source.Reserve.Value, "0") {
				bound = true
			}
		}
		if !bound {
			return false
		}
	}
	return len(m.Lines) > 0
}

func nativeUnpostedPosition(position TargetPosition) bool {
	return position.Reserve.Validate() == nil && position.Shipped.Validate() == nil && position.Reserve.IsKnown() && position.Shipped.IsKnown() && ExactDecimalEqual(*position.Reserve.Value, "0") && ExactDecimalEqual(*position.Shipped.Value, "0")
}
