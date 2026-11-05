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
	first, found := splits[f.Deferred.Spine[0].Work.ID]
	if !present || !found || first.CreatedAt.Before(origin.ReconciledAt) {
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
