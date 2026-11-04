package orders

import "slices"

const ActionMirrorCompositionUpdate Action = "mirror_composition_update"

type CompositionProposal struct {
	PostingNumber      string `json:"posting_number"`
	ConfirmReplacement bool   `json:"confirm_replacement"`
}

type PositionIdentityRef struct {
	SourceIdentity   LineIdentity `json:"source_identity"`
	NativePositionID string       `json:"native_position_id"`
}

// PositionReplacement is destructive collection consent, not a scalar patch.
// The entire native frontier must already have independent ownership evidence.
type PositionReplacement struct {
	Baseline           PositionBaseline      `json:"baseline"`
	ConfirmReplacement bool                  `json:"confirm_replacement"`
	Retained           []PositionIdentityRef `json:"retained"`
	Added              []LineIdentity        `json:"added"`
	Removed            []PositionIdentityRef `json:"removed"`
	Applicable         Field[bool]           `json:"applicable"`
	VATEnabled         Field[bool]           `json:"vat_enabled"`
}

func PlanPositionReplacement(command Command, baseline PositionBaseline, vatEnabled, applicable Field[bool]) (*PositionReplacement, error) {
	if command.CustomerOrder == nil || baseline.Validate() != nil || baseline.Scope != command.Scope || baseline.Correlation != command.Correlation || baseline.RecoveryID != "" || baseline.EvidenceClass != "" && baseline.EvidenceClass != NativeDemandReadback || !command.TargetPositionsComplete || len(baseline.Bindings) != len(command.TargetPositions) || len(command.TargetPositions) == 0 || !vatEnabled.IsKnown() || !applicable.IsKnown() {
		return nil, ErrBlocked
	}
	result := &PositionReplacement{Baseline: baseline, ConfirmReplacement: true, VATEnabled: vatEnabled, Applicable: applicable, Retained: []PositionIdentityRef{}, Added: []LineIdentity{}, Removed: []PositionIdentityRef{}}
	if err := replacementFrontier(command, baseline); err != nil {
		return nil, err
	}
	if err := replacementWanted(command, result); err != nil {
		return nil, err
	}
	for _, binding := range baseline.Bindings {
		if !slices.ContainsFunc(command.CustomerOrder.Positions, func(w PositionWrite) bool { return w.Identity.Key() == binding.Source.Identity.Key() }) {
			result.Removed = append(result.Removed, PositionIdentityRef{binding.Source.Identity, binding.Native.ID})
		}
	}
	if len(result.Added)+len(result.Removed) == 0 {
		return nil, ErrBlocked
	}
	return result, nil
}

func replacementWanted(command Command, result *PositionReplacement) error {
	baseline := result.Baseline
	seen := make(map[string]bool, len(command.CustomerOrder.Positions))
	for _, wanted := range command.CustomerOrder.Positions {
		key := wanted.Mapping.MoyskladKind + ":" + wanted.Mapping.MoyskladID
		if seen[key] || !wanted.Price.IsKnown() || !wanted.Reserve.IsKnown() || !wanted.VAT.IsKnown() || !ExactDecimalEqual(*wanted.VAT.Value, "0") || !nonnegativeNotGreater(*wanted.Reserve.Value, wanted.Quantity) {
			return ErrBlocked
		}
		seen[key] = true
		binding, found := replacementBinding(baseline, wanted.Identity)
		if !found {
			result.Added = append(result.Added, wanted.Identity)
			continue
		}
		if !positionMappingsEqual(binding.Source.Mapping, wanted.Mapping) {
			return ErrStale
		}
		current, _ := nativePositionByID(command.TargetPositions, binding.Native.ID)
		if !ExactDecimalEqual(*current.Discount.Value, "0") && !desiredMoney(wanted.Price, current.Price) {
			return ErrBlocked
		}
		result.Retained = append(result.Retained, PositionIdentityRef{wanted.Identity, binding.Native.ID})
	}
	return nil
}

func replacementFrontier(command Command, baseline PositionBaseline) error {
	for _, binding := range baseline.Bindings {
		current, found := nativePositionByID(command.TargetPositions, binding.Native.ID)
		if !found || !OwnedPositionFrontierMatches(binding.Native, current) || !current.CompositionSafe.IsKnown() || !*current.CompositionSafe.Value || !current.Shipped.IsKnown() || !ExactDecimalEqual(*current.Shipped.Value, "0") || !current.Reserve.IsKnown() || !current.Discount.IsKnown() || !current.VAT.IsKnown() || !current.VATEnabled.IsKnown() || !current.Price.IsKnown() || !ExactDecimalEqual(*current.VAT.Value, "0") {
			return ErrBlocked
		}
		if !ExactDecimalEqual(*current.Reserve.Value, "0") && (!binding.Source.Reserve.IsKnown() || !desiredDecimal(binding.Source.Reserve, current.Reserve)) {
			return ErrBlocked
		}
	}
	return nil
}

func replacementBinding(baseline PositionBaseline, identity LineIdentity) (PositionBinding, bool) {
	for _, binding := range baseline.Bindings {
		if binding.Source.Identity.Key() == identity.Key() {
			return binding, true
		}
	}
	return PositionBinding{}, false
}

func (replacement PositionReplacement) Validate(command Command) error {
	if command.Action != ActionMirrorCompositionUpdate || !replacement.ConfirmReplacement || command.PositionBaseline != nil || command.PositionPatch != nil || command.AutomationGrantRevision != 0 || command.CustomerOrder == nil || !slices.Equal(command.CustomerOrder.OwnedFields, []string{"positions.quantity", "positions.reserve", "positions.price", "positions.vat"}) {
		return ErrBlocked
	}
	expected, err := PlanPositionReplacement(command, replacement.Baseline, replacement.VATEnabled, replacement.Applicable)
	if err != nil || !positionJSONEqual(expected, &replacement) {
		return ErrStale
	}
	return nil
}

func CompositionTargetCurrent(command Command, target TargetObservation) error {
	if command.PositionReplacement == nil || command.PositionReplacement.Validate(command) != nil {
		return ErrBlocked
	}
	if !target.Complete || target.Scope != command.Scope || target.Correlation != command.Correlation || target.Revision != command.ExpectedTargetRevision || !NativePositionsEqual(target.Positions, command.TargetPositions) || !nativeBoolEqual(command.PositionReplacement.VATEnabled, target.Fields.VATEnabled) || !nativeBoolEqual(command.PositionReplacement.Applicable, target.Fields.Applicable) {
		return ErrStale
	}
	for _, current := range target.Positions {
		if !current.CompositionSafe.IsKnown() || !*current.CompositionSafe.Value {
			return ErrBlocked
		}
	}
	return nil
}

// Complete readback keeps retained UUIDs and refuses any extra or stale row.
func CompositionTargetObserved(command Command, target TargetObservation) bool {
	if command.PositionReplacement == nil || command.PositionReplacement.Validate(command) != nil || !target.Complete || target.Scope != command.Scope || target.Correlation != command.Correlation || len(target.Positions) != len(command.CustomerOrder.Positions) || !nativeBoolEqual(command.PositionReplacement.Applicable, target.Fields.Applicable) || !nativeBoolEqual(command.PositionReplacement.VATEnabled, target.Fields.VATEnabled) {
		return false
	}
	for _, wanted := range command.CustomerOrder.Positions {
		index := uniqueNativePosition(wanted, target.Positions)
		if index < 0 || !PositionRepresented(wanted, target.Positions[index]) {
			return false
		}
		observed := target.Positions[index]
		if !observed.CompositionSafe.IsKnown() || !*observed.CompositionSafe.Value || !observed.Shipped.IsKnown() || !ExactDecimalEqual(*observed.Shipped.Value, "0") {
			return false
		}
		prior, retained := replacementBinding(command.PositionReplacement.Baseline, wanted.Identity)
		if retained {
			current, _ := nativePositionByID(command.TargetPositions, prior.Native.ID)
			if observed.ID != prior.Native.ID || !SameObservedDecimal(observed.Discount, current.Discount) || !nativeBoolEqual(observed.VATEnabled, current.VATEnabled) {
				return false
			}
			continue
		}
		if slices.ContainsFunc(command.TargetPositions, func(old TargetPosition) bool { return old.ID == observed.ID }) || !observed.Discount.IsKnown() || !ExactDecimalEqual(*observed.Discount.Value, "0") || !nativeBoolEqual(observed.VATEnabled, command.PositionReplacement.VATEnabled) {
			return false
		}
	}
	for _, removed := range command.PositionReplacement.Removed {
		if _, found := nativePositionByID(target.Positions, removed.NativePositionID); found {
			return false
		}
	}
	return true
}

func CompositionDispatchAcknowledged(work Work) bool {
	receipt := work.Receipt
	return work.DispatchCount == 1 && receipt.State == StateAcknowledged && receipt.HTTPStatus >= 200 && receipt.HTTPStatus < 300 && receipt.CommandID == work.Command.ID && receipt.ExternalID == work.Command.Correlation.CustomerOrderID && receipt.ExternalID != ""
}
