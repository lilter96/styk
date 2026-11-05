package orders

func retainedHistoryClocks(f SplitAncestorSettlementFacts) bool {
	splits, ok := deepAncestorTimes(f.Attempts, f.CheckedAt)
	if !ok {
		return false
	}
	native, ok := deepAncestorTimes(f.NativeAttempts, f.CheckedAt)
	if !ok {
		return false
	}
	expected := make(map[string]bool)
	var previous *Work
	for index, generation := range f.RetainedHistory {
		origin, found := native[generation.Origin.ID]
		split, present := splits[generation.Split.Work.ID]
		if !found || !present || split.CreatedAt.Before(origin.ReconciledAt) {
			return false
		}
		if index > 0 && !retainedHistoryPreviousClock(f.RetainedHistory[index-1], generation, previous, origin, native) {
			return false
		}
		expected[generation.Origin.ID] = true
		if generation.Reduction != nil {
			reduction, found := native[generation.Reduction.ID]
			if !found || reduction.CreatedAt.Before(split.ReconciledAt) {
				return false
			}
			expected[generation.Reduction.ID] = true
		}
		previous = generation.Reduction
	}
	return len(expected) == len(native)
}

func retainedHistoryPreviousClock(before, next SplitRetainedGeneration, previous *Work, origin NestedSplitAttempt, native map[string]NestedSplitAttempt) bool {
	if previous == nil {
		return false
	}
	settled, found := native[previous.ID]
	if !found {
		return false
	}
	if before.Split.Before.PostingNumber == next.Split.Before.PostingNumber {
		return next.Origin.ID == previous.ID
	}
	return !origin.CreatedAt.Before(settled.ReconciledAt)
}
