package orders

import "time"

func deepAncestorChronology(f SplitAncestorSettlementFacts) bool {
	splitTimes, ok := deepAncestorTimes(f.Attempts, f.CheckedAt)
	if !ok {
		return false
	}
	nativeTimes, ok := deepAncestorTimes(f.NativeAttempts, f.CheckedAt)
	if !ok {
		return false
	}
	rootSplit, splitOK := splitTimes[f.RootTransfer.Split.ID]
	rootOrigin, originOK := nativeTimes[f.RootOrigin.ID]
	rootReduction, reductionOK := nativeTimes[f.RootTransfer.ParentRelease.ID]
	if !splitOK || !originOK || !reductionOK || rootSplit.CreatedAt.Before(rootOrigin.ReconciledAt) || rootReduction.CreatedAt.Before(rootSplit.ReconciledAt) {
		return false
	}
	return deepAncestorGenerationClocks(f.Path, splitTimes, nativeTimes, rootReduction)
}

func deepAncestorGenerationClocks(path []SplitAncestorGeneration, splitTimes, nativeTimes map[string]NestedSplitAttempt, previousReduction NestedSplitAttempt) bool {
	expected := 2
	for _, generation := range path {
		if previousReduction.WorkID == "" {
			return false
		}
		origin, found := nativeTimes[generation.Origin.ID]
		split, present := splitTimes[generation.Split.Work.ID]
		if !found || !present || origin.CreatedAt.Before(previousReduction.ReconciledAt) || split.CreatedAt.Before(origin.ReconciledAt) {
			return false
		}
		expected++
		if generation.Reduction != nil {
			reduction, found := nativeTimes[generation.Reduction.ID]
			if !found || reduction.CreatedAt.Before(split.ReconciledAt) {
				return false
			}
			previousReduction = reduction
			expected++
		} else {
			previousReduction = NestedSplitAttempt{}
		}
	}
	return len(nativeTimes) == expected
}

func deepAncestorTimes(attempts []NestedSplitAttempt, checked time.Time) (map[string]NestedSplitAttempt, bool) {
	result := make(map[string]NestedSplitAttempt, len(attempts))
	for _, attempt := range attempts {
		if !validID(attempt.WorkID) || attempt.CreatedAt.IsZero() || attempt.DispatchedAt.IsZero() || attempt.ReconciledAt.IsZero() || attempt.DispatchedAt.Before(attempt.CreatedAt) || attempt.ReconciledAt.Before(attempt.DispatchedAt) || attempt.ReconciledAt.After(checked.Add(5*time.Second)) {
			return nil, false
		}
		if _, exists := result[attempt.WorkID]; exists {
			return nil, false
		}
		result[attempt.WorkID] = attempt
	}
	return result, true
}
