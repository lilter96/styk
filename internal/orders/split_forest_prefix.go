package orders

import "time"

// An immutable source prefix cannot authorize a native write while the same
// posting is being split. Other identities may progress independently.
func forestNativeSplitClocks(f SplitForestFacts, event SplitForestNative) bool {
	for _, split := range f.Lineage.Splits {
		if split.Before.PostingNumber != event.Source.PostingNumber {
			continue
		}
		attempt, found := forestAttempt(f, split.Work.ID)
		if !found || attempt.CreatedAt.Before(event.Attempt.ReconciledAt) && event.Attempt.CreatedAt.Before(attempt.ReconciledAt) {
			return false
		}
	}
	return true
}

func forestAttempt(f SplitForestFacts, id string) (NestedSplitAttempt, bool) {
	for _, attempt := range f.Attempts {
		if attempt.WorkID == id {
			return attempt, true
		}
	}
	return NestedSplitAttempt{}, false
}

func forestHead(f SplitForestFacts, number string) Posting {
	for _, head := range f.Lineage.CurrentHeads {
		if head.PostingNumber == number {
			return head
		}
	}
	return Posting{}
}

func forestPrefix(f SplitForestFacts, number string, cut time.Time) Posting {
	var found Posting
	var latest time.Time
	for _, step := range f.Lineage.Splits {
		attempt, ok := forestAttempt(f, step.Work.ID)
		if !ok || attempt.ReconciledAt.After(cut) {
			continue
		}
		output := ancestorNodeOutput(step.Work, number)
		if output.PostingNumber != "" && found.PostingNumber != "" && attempt.CreatedAt.Equal(latest) {
			return Posting{} // A clock tie cannot choose a historical generation.
		}
		if output.PostingNumber != "" && (found.PostingNumber == "" || attempt.CreatedAt.After(latest)) {
			found, latest = output, attempt.CreatedAt
		}
	}
	return found
}

func forestPrevious(f SplitForestFacts, number string, cut time.Time, self string) (SplitForestNative, bool) {
	var found SplitForestNative
	for _, event := range f.Native {
		if event.Work.ID == self || event.Source.PostingNumber != number || !event.Attempt.CreatedAt.Before(cut) {
			continue
		}
		if event.Attempt.ReconciledAt.After(cut) {
			return SplitForestNative{}, false
		}
		if found.Work.ID != "" && event.Attempt.CreatedAt.Equal(found.Attempt.CreatedAt) {
			return SplitForestNative{}, false
		}
		if found.Work.ID == "" || event.Attempt.CreatedAt.After(found.Attempt.CreatedAt) {
			found = event
		}
	}
	return found, found.Work.ID != ""
}

func forestSplit(f SplitForestFacts, id string) (SplitLineageStep, bool) {
	for _, step := range f.Lineage.Splits {
		if step.Work.ID == id {
			return step, true
		}
	}
	return SplitLineageStep{}, false
}

func forestBirth(f SplitForestFacts, number string) (SplitLineageStep, bool) {
	for _, step := range f.Lineage.Splits {
		if step.Before.PostingNumber != number && ancestorNodeOutput(step.Work, number).PostingNumber != "" {
			return step, true
		}
	}
	return SplitLineageStep{}, false
}
