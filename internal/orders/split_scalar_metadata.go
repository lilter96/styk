package orders

// Metadata observations preserve the preceding inventory authority. They are
// neither a native creation nor a reserve reduction; recreated UUIDs without
// an explicit unchanged ownership baseline remain an ownership break.
func splitScalarMetadataObserved(w Work, target TargetObservation, prior PositionBaseline) bool {
	c := w.Command
	if !splitScalarMetadataCommand(c) || !c.PositionBaseline.Equal(prior) || !ancestorNativeAcknowledged(w, target) {
		return false
	}
	return NativePositionsEqual(c.TargetPositions, target.Positions) && ScalarPositionFrontier(c, prior, target.Positions)
}

func splitScalarMetadataCommand(c Command) bool {
	if c.Action != ActionMirrorUpdate || c.CustomerOrder == nil || c.PositionBaseline == nil || c.Validate() != nil || !splitMetadataFields(c.CustomerOrder.OwnedFields) {
		return false
	}
	prior := *c.PositionBaseline
	return prior.RecoveryID == "" && prior.EvidenceClass != "OPERATOR_ATTESTED" && ScalarPositionFrontier(c, prior, c.TargetPositions) && splitMetadataSourcesUnchanged(c.CustomerOrder.Positions, prior)
}

// SplitScalarMetadataAuthority checks a scoped, actual inventory operation and
// its reconciliation before the store considers excluding an exact unsent
// metadata self from history. It grants no dispatch or absence by itself.
// A metadata ACK can observe authority but can never become its creator.
func SplitScalarMetadataAuthority(c Command, authority Work, target TargetObservation) bool {
	if !splitScalarMetadataCommand(c) || authority.ID != c.PositionBaseline.WorkID || authority.Command.Action == ActionMirrorUpdate || !ancestorNativeAcknowledged(authority, target) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(authority, target)
	return err == nil && baseline.Equal(*c.PositionBaseline)
}

func splitMetadataFields(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		switch field {
		case "name", "description", "moment", "delivery_at", "state", "attribute", "shipment_address":
			continue
		default:
			if !ShipmentAddressTargetValid(field) {
				return false
			}
		}
	}
	return true
}

func splitMetadataSourcesUnchanged(wanted []PositionWrite, prior PositionBaseline) bool {
	if len(wanted) != len(prior.Bindings) {
		return false
	}
	seen := make(map[string]bool, len(wanted))
	for _, position := range wanted {
		binding, found := replacementBinding(prior, position.Identity)
		if !found || seen[position.Identity.Key()] || position.Identity != binding.Source.Identity || !positionMappingsEqual(position.Mapping, binding.Source.Mapping) || !ExactDecimalEqual(position.Quantity, binding.Source.Quantity) || !SameObservedDecimal(position.Reserve, binding.Source.Reserve) || !SameObservedDecimal(position.VAT, binding.Source.VAT) || !sameObservedPrice(position.Price, binding.Source.Price) {
			return false
		}
		seen[position.Identity.Key()] = true
	}
	return true
}

func forestInventoryAuthority(f SplitForestFacts, observed SplitForestNative, baseline PositionBaseline) bool {
	for _, authority := range f.Native {
		if authority.Work.ID != baseline.WorkID {
			continue
		}
		if authority.Source.PostingNumber != observed.Source.PostingNumber || authority.Work.Command.Action == ActionMirrorUpdate || authority.Attempt.ReconciledAt.After(observed.Attempt.ReconciledAt) {
			return false
		}
		actual, err := ProjectionPositionBaseline(authority.Work, authority.Target)
		return err == nil && baseline.Equal(actual)
	}
	return false
}

func splitMetadataAuthorityTarget(baseline PositionBaseline, target TargetObservation) bool {
	for _, binding := range baseline.Bindings {
		current, found := nativePositionByID(target.Positions, binding.Native.ID)
		if !found || !OwnedPositionFrontierMatches(binding.Native, current) {
			return false
		}
	}
	return true
}
