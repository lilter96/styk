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
		step, found := deferredReductionStep(f, reduction.Work)
		if !present || !found || used[timing.WorkID] || timing.CreatedAt.Before(previous.ReconciledAt) || timing.CreatedAt.Before(splits[step.Work.ID].ReconciledAt) {
			return false
		}
		previous, used[timing.WorkID] = timing, true
	}
	return len(used) == len(native)
}

const (
	deferredOriginPreSplit = iota + 1
	deferredOriginPostFirstSplit
)

// DeferredOriginChronologySupported checks only the immutable database-clock
// cut. It grants no reserve authority: source allocation, complete native
// history and exact binding must still pass NewSplitAncestorSettlement.
func DeferredOriginChronologySupported(f SplitAncestorSettlementFacts) bool {
	return deferredOriginCut(f) != 0
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
	first, found := splits[d.Spine[0].Work.ID]
	if !present || !found {
		return 0
	}
	if !first.CreatedAt.Before(origin.ReconciledAt) {
		return deferredOriginPreSplit
	}
	next, found := splits[d.Spine[1].Work.ID]
	if found && !origin.CreatedAt.Before(first.ReconciledAt) && !next.CreatedAt.Before(origin.ReconciledAt) {
		return deferredOriginPostFirstSplit
	}
	return 0
}
