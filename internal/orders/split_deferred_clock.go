package orders

func deferredClocks(f SplitAncestorSettlementFacts) bool {
	splits, ok := deepAncestorTimes(f.Attempts, f.CheckedAt)
	if !ok {
		return false
	}
	native, ok := deepAncestorTimes(f.NativeAttempts, f.CheckedAt)
	if !ok || len(native) != len(f.Deferred.Reductions)+1 {
		return false
	}
	origin, present := native[f.Deferred.Origin.Work.ID]
	if !present || deferredOriginClockCut(f, splits, native) == 0 {
		return false
	}
	used := map[string]bool{origin.WorkID: true}
	previous := origin
	for _, reduction := range f.Deferred.Reductions {
		timing, present := native[reduction.Work.ID]
		if !present || used[timing.WorkID] || timing.CreatedAt.Before(previous.ReconciledAt) || !deferredNativeEventClocks(f, reduction.Work, timing, splits) {
			return false
		}
		previous, used[timing.WorkID] = timing, true
	}
	return len(used) == len(native)
}

func deferredNativeEventClocks(f SplitAncestorSettlementFacts, work Work, timing NestedSplitAttempt, splits map[string]NestedSplitAttempt) bool {
	if work.Command.Action == ActionMirrorUpdate {
		return deferredMetadataClocks(f, timing, splits)
	}
	step, found := deferredReductionStep(f, work)
	return found && !timing.CreatedAt.Before(splits[step.Work.ID].ReconciledAt)
}

const (
	deferredOriginPreSplit       = iota + 1
	deferredOriginPostFirstSplit // Later cuts are this value plus the spine index.
)

// DeferredOriginChronologySupported selects bounded same-root origin histories,
// not their validity. An invalid or overlapping clock must reach the settlement
// factory and block competing permissions, not select a legacy fallback.
// The factory validates every split cut, source allocation and native binding.
func DeferredOriginChronologySupported(f SplitAncestorSettlementFacts) bool {
	d := f.Deferred
	if d == nil || len(d.Spine) < 2 {
		return false
	}
	_, present := deferredOriginAttempt(f.NativeAttempts, d.Origin.Work.ID)
	_, found := deferredOriginAttempt(f.Attempts, d.Spine[0].Work.ID)
	return present && found
}

func deferredOriginAttempt(attempts []NestedSplitAttempt, id string) (NestedSplitAttempt, bool) {
	for _, attempt := range attempts {
		if attempt.WorkID == id {
			return attempt, true
		}
	}
	return NestedSplitAttempt{}, false
}

func deferredOriginCut(f SplitAncestorSettlementFacts) int {
	if f.CheckedAt.IsZero() {
		return 0
	}
	splits, ok := deepAncestorTimes(f.Attempts, f.CheckedAt)
	if !ok {
		return 0
	}
	native, ok := deepAncestorTimes(f.NativeAttempts, f.CheckedAt)
	if !ok {
		return 0
	}
	return deferredOriginClockCut(f, splits, native)
}

func deferredOriginClockCut(f SplitAncestorSettlementFacts, splits, native map[string]NestedSplitAttempt) int {
	d := f.Deferred
	if d == nil || len(d.Spine) < 2 {
		return 0
	}
	origin, present := native[d.Origin.Work.ID]
	if !present {
		return 0
	}
	cut := deferredOriginPreSplit
	for index, step := range d.Spine {
		timing, found := splits[step.Work.ID]
		if !found || timing.CreatedAt.Before(origin.ReconciledAt) && origin.CreatedAt.Before(timing.ReconciledAt) {
			return 0 // No provider/native mutation may straddle a split.
		}
		if !origin.CreatedAt.Before(timing.ReconciledAt) {
			cut = deferredOriginPostFirstSplit + index
		}
	}
	return cut
}
