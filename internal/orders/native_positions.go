package orders

import "time"

const NativeDemandReadback = "NATIVE_DEMAND_READBACK"

// NativePositionBaseline learns counters only from a separately reconciled
// native demand. Its original source bindings remain in the immutable intent;
// this is neither a new mirror nor a promotion of operator-attested ownership.
func NativePositionBaseline(work Work, target TargetObservation, document NativeDocumentObservation, confirmed time.Time) (PositionBaseline, error) {
	movement := work.Command.NativeMovement
	if work.Command.Validate() != nil || work.State != StateReconciled || work.Scope != work.Command.Scope || work.Command.Action != ActionDemandCreate || movement == nil || !movement.Applicable.IsKnown() || !*movement.Applicable.Value || confirmed.IsZero() || target.ObservedAt.After(confirmed) || document.ObservedAt.After(confirmed) {
		return PositionBaseline{}, ErrBlocked
	}
	original := movement.SourceBaseline
	if original.RecoveryID != "" || original.EvidenceClass != "" && original.EvidenceClass != NativeDemandReadback || original.Validate() != nil {
		return PositionBaseline{}, ErrBlocked
	}
	if !NativeDocumentMatches(work.Command, document) || !NativeParentReadback(work.Command, target) || target.ProviderUpdatedAt.Validate() != nil || !target.ProviderUpdatedAt.IsKnown() {
		return PositionBaseline{}, ErrStale
	}
	result := PositionBaseline{Scope: work.Scope, WorkID: work.ID, CommandID: work.Command.ID, Correlation: target.Correlation, EvidenceClass: NativeDemandReadback, ObservedAt: confirmed, Bindings: make([]PositionBinding, 0, len(original.Bindings))}
	selected := make(map[string]bool, len(movement.Lines))
	for _, line := range movement.Lines {
		selected[line.OrderPositionID] = true
	}
	for _, binding := range original.Bindings {
		native, found := nativePositionByID(target.Positions, binding.Native.ID)
		if !found {
			return PositionBaseline{}, ErrStale
		}
		if selected[native.ID] && binding.Source.Reserve.IsKnown() {
			binding.Source.Reserve = native.Reserve
		}
		binding.Native = native
		result.Bindings = append(result.Bindings, binding)
	}
	return result, result.Validate()
}

func nativePositionByID(positions []TargetPosition, id string) (TargetPosition, bool) {
	var result TargetPosition
	found := false
	for _, position := range positions {
		if position.ID != id {
			continue
		}
		if found {
			return TargetPosition{}, false
		}
		result, found = position, true
	}
	return result, found
}

// NativeParentVersionTime parses only the documented MoySklad timestamp forms.
// Its clock is the provider's; this is not a database admission timestamp.
func NativeParentVersionTime(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
		parsed, err := time.ParseInLocation(layout, value, time.FixedZone("Moscow", 3*60*60))
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, ErrInvalid
}
