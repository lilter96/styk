package orders

import "time"

// A leaf's first retained generation is its exact birth output. Once it has
// split, the latest actual own split supplies Before. Neither case fabricates
// an earlier native document or changes a historical source revision.
func forestNodeGeneration(f SplitForestFacts, number string, cut time.Time) (SplitLineageStep, bool) {
	if step, found := forestLastSplit(f, number, cut); found {
		return step, true
	}
	birth, found := forestBirth(f, number)
	if !found {
		return SplitLineageStep{}, false
	}
	attempt, exists := forestAttempt(f, birth.Work.ID)
	if !exists || attempt.ReconciledAt.After(cut) {
		return SplitLineageStep{}, false
	}
	birth.Before = ancestorNodeOutput(birth.Work, number)
	return birth, birth.Before.PostingNumber == number
}

func forestCurrentSourcePath(f SplitForestFacts, number string) []Posting {
	chain := forestChain(f, number)
	if len(chain) < 2 {
		return nil
	}
	path := make([]Posting, 0, len(chain)-1)
	for _, ancestor := range chain[:len(chain)-1] {
		path = append(path, minimalSupersededPosting(forestHead(f, ancestor)))
	}
	return path
}

func forestGenerationBeforeBound(f SplitForestFacts, before Posting, owner string, cut time.Time) bool {
	if forestBeforeBound(f, before, owner, cut) {
		return true
	}
	if _, split := forestLastSplit(f, owner, cut); split {
		return false
	}
	generation, found := forestNodeGeneration(f, owner, cut)
	return found && splitTransferPostingEqual(minimalSupersededPosting(generation.Before), before)
}
