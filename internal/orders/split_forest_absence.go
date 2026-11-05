package orders

import "time"

func forestNativeHistory(f SplitForestFacts, number string) (NestedSplitNativeHistory, bool) {
	var result NestedSplitNativeHistory
	found := false
	for _, history := range f.NativeHistory {
		if history.PostingNumber != number {
			continue
		}
		if found || history.Scope != f.Lineage.Scope || history.BindingIDs == nil || history.MirrorWorkIDs == nil {
			return NestedSplitNativeHistory{}, false
		}
		result, found = history, true
	}
	return result, found
}

func forestCurrentNativeFree(f SplitForestFacts, number string) bool {
	history, found := forestNativeHistory(f, number)
	if !found || len(history.BindingIDs) != 0 || len(history.MirrorWorkIDs) != 0 {
		return false
	}
	for _, event := range f.Native {
		if event.Source.PostingNumber == number {
			return false
		}
	}
	return true
}

// Later real admissions do not erase an earlier native-free prefix. Bindings
// and the bounded work-existence witness must be explained by complete actual
// reconciled events after the historical cut; an orphan is never absence.
func forestHistoricalNativeFree(f SplitForestFacts, number string, cut time.Time) bool {
	history, found := forestNativeHistory(f, number)
	if !found {
		return false
	}
	works, bindings := make(map[string]bool), make(map[string]bool)
	for _, event := range f.Native {
		if event.Source.PostingNumber != number {
			continue
		}
		if !event.Attempt.CreatedAt.After(cut) || !ancestorNativeAcknowledged(event.Work, event.Target) {
			return false
		}
		works[event.Work.ID] = true
		if event.Work.Command.Action == ActionMirrorCreate {
			bindings[event.Target.Correlation.CustomerOrderID] = true
		}
	}
	return forestAbsenceWitnessMatches(history, works, bindings)
}

func forestAbsenceWitnessMatches(history NestedSplitNativeHistory, works, bindings map[string]bool) bool {
	if (len(history.MirrorWorkIDs) == 0) != (len(works) == 0) || len(history.BindingIDs) != len(bindings) {
		return false
	}
	for _, id := range history.MirrorWorkIDs {
		if !works[id] {
			return false
		}
	}
	for _, id := range history.BindingIDs {
		if !bindings[id] {
			return false
		}
		delete(bindings, id)
	}
	return len(bindings) == 0
}
