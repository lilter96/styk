package orders

import (
	"slices"
	"time"
)

func forestGrant(f SplitForestFacts, e SplitForestNative, p SplitAncestorSettlementPermission) bool {
	owner := e.Source.PostingNumber
	if p.Mode == SplitAncestorAdmit {
		birth, found := forestBirth(f, owner)
		if !found {
			return false
		}
		owner = birth.Before.PostingNumber
	}
	previous, found := forestPrevious(f, owner, e.Attempt.CreatedAt, e.Work.ID)
	if !found || p.RootSplitWorkID != forestRoot(f).Work.ID || p.Node.PostingNumber != owner || !p.Allows(e.Source) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(previous.Work, previous.Target)
	step, exists := forestNodeGeneration(f, owner, e.Attempt.CreatedAt)
	if err != nil || !baseline.Equal(p.Baseline) || !exists || step.Work.ID != p.SplitWorkID || !forestGenerationBeforeBound(f, p.Before, owner, e.Attempt.CreatedAt) {
		return false
	}
	if !splitTransferSameAllocation(forestPrefix(f, owner, e.Attempt.CreatedAt), p.Node) || p.Mode == SplitAncestorAdmit && p.NodeReductionWorkID != baseline.WorkID {
		return false
	}
	if p.Mode == SplitAncestorAdmit && !splitTransferAllParentLinesReserved(p.Node, baseline) {
		return false
	}
	return forestGrantAncestors(f, e, p, owner) && forestNativeIDBefore(f, p.NodeOriginWorkID, owner, e.Attempt.CreatedAt)
}

func forestGrantAncestors(f SplitForestFacts, e SplitForestNative, p SplitAncestorSettlementPermission, owner string) bool {
	chain := forestChain(f, owner)
	if len(chain) == 0 || !forestSourcePathAt(f, p, e.Attempt.CreatedAt) {
		return false
	}
	next := 0
	for _, number := range chain {
		native, found := forestPrevious(f, number, e.Attempt.CreatedAt, e.Work.ID)
		if !found {
			if len(p.SourceAncestors) == 0 || !forestHistoricalNativeFree(f, number, e.Attempt.CreatedAt) {
				return false
			}
			continue
		}
		if next >= len(p.Ancestors) || !forestGrantAncestorMatches(f, e, p.Ancestors[next], native, number) {
			return false
		}
		if len(p.SourceAncestors) > 0 && number != owner && !forestNativeAncestorSettled(f, p.Ancestors[next], native) {
			return false
		}
		next++
	}
	return next == len(p.Ancestors)
}

func forestNativeAncestorSettled(f SplitForestFacts, a SplitAncestorNative, native SplitForestNative) bool {
	baseline, err := ProjectionPositionBaseline(native.Work, native.Target)
	return err == nil && forestInventoryAuthority(f, native, baseline) && splitTransferAllParentLinesReserved(a.Posting, baseline)
}

func forestGrantAncestorMatches(f SplitForestFacts, e SplitForestNative, a SplitAncestorNative, native SplitForestNative, number string) bool {
	return a.Posting.PostingNumber == number && a.WorkID == native.Work.ID && a.TargetMatches(native.Target) && a.Target.ObservedAt.Equal(native.Target.ObservedAt) && splitTransferSameAllocation(forestPrefix(f, number, e.Attempt.CreatedAt), a.Posting)
}

func forestChain(f SplitForestFacts, number string) []string {
	seen := make(map[string]bool)
	chain := make([]string, 0, MaxSplitLineageDepth)
	for number != "" && len(chain) < MaxSplitLineageDepth {
		if seen[number] {
			return nil
		}
		seen[number] = true
		chain = append(chain, number)
		if number == f.Lineage.RootPosting {
			slices.Reverse(chain)
			return chain
		}
		birth, found := forestBirth(f, number)
		if !found {
			return nil
		}
		number = birth.Before.PostingNumber
	}
	return nil
}

func forestNativeIDBefore(f SplitForestFacts, id, number string, cut time.Time) bool {
	for _, event := range f.Native {
		if event.Work.ID == id {
			return event.Source.PostingNumber == number && !event.Attempt.ReconciledAt.After(cut)
		}
	}
	return false
}

func forestLastSplit(f SplitForestFacts, number string, cut time.Time) (SplitLineageStep, bool) {
	var result SplitLineageStep
	var latest time.Time
	for _, step := range f.Lineage.Splits {
		a, found := forestAttempt(f, step.Work.ID)
		if found && step.Before.PostingNumber == number && !a.ReconciledAt.After(cut) && (result.Work.ID == "" || a.CreatedAt.After(latest)) {
			result, latest = step, a.CreatedAt
		}
	}
	return result, result.Work.ID != ""
}

func forestBeforeBound(f SplitForestFacts, before Posting, owner string, cut time.Time) bool {
	for _, step := range f.Lineage.Splits {
		a, found := forestAttempt(f, step.Work.ID)
		if found && !a.ReconciledAt.After(cut) && step.Before.PostingNumber == owner && splitTransferPostingEqual(step.Before, before) {
			return true
		}
	}
	return false
}
