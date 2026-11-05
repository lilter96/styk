package orders

import "time"

func forestNodeUnmirrored(f SplitForestFacts, number string, cut time.Time, self string) bool {
	for _, event := range f.Native {
		if event.Work.ID != self && event.Source.PostingNumber == number && !event.Attempt.CreatedAt.After(cut) {
			return false
		}
	}
	found := false
	for _, step := range f.Lineage.Splits {
		if step.Before.PostingNumber != number {
			continue
		}
		a, exists := forestAttempt(f, step.Work.ID)
		if !exists {
			return false
		}
		if !a.ReconciledAt.After(cut) {
			if step.Work.Command.Correlation.CustomerOrderID != "" {
				return false
			}
			found = true
		}
	}
	return found
}

func forestNodeCreation(f SplitForestFacts, e SplitForestNative, p SplitAncestorSettlementPermission) bool {
	cut := e.Attempt.CreatedAt
	if !p.Allows(e.Source) || !p.AllowsCommand(e.Work.Command) || p.RootSplitWorkID != forestRoot(f).Work.ID || !forestNodeUnmirrored(f, e.Source.PostingNumber, cut, e.Work.ID) {
		return false
	}
	step, exists := forestLastSplit(f, e.Source.PostingNumber, cut)
	if !exists || step.Work.ID != p.SplitWorkID || !splitTransferPostingEqual(minimalSupersededPosting(step.Before), p.Before) || !splitTransferSameAllocation(forestPrefix(f, e.Source.PostingNumber, cut), e.Source) {
		return false
	}
	chain := forestChain(f, e.Source.PostingNumber)
	if len(chain) < 2 || len(p.Ancestors) != len(chain)-1 {
		return false
	}
	var parent PositionBaseline
	for i, number := range chain[:len(chain)-1] {
		baseline, valid := forestNodeAncestorAt(f, e, p.Ancestors[i], number)
		if !valid {
			return false
		}
		parent = baseline
	}
	return p.Baseline.Equal(parent) && p.NodeOriginWorkID == parent.WorkID
}

func forestNodeAncestorAt(f SplitForestFacts, e SplitForestNative, a SplitAncestorNative, number string) (PositionBaseline, bool) {
	native, found := forestPrevious(f, number, e.Attempt.CreatedAt, e.Work.ID)
	if !found || a.Posting.PostingNumber != number || a.WorkID != native.Work.ID || !a.TargetMatches(native.Target) || !a.Target.ObservedAt.Equal(native.Target.ObservedAt) {
		return PositionBaseline{}, false
	}
	posting := forestPrefix(f, number, e.Attempt.CreatedAt)
	baseline, err := ProjectionPositionBaseline(native.Work, native.Target)
	// The split prefix proves allocation, not the later observed status. Keep
	// reserve semantics bound to the actual immutable source sealed in the grant.
	return baseline, err == nil && forestInventoryAuthority(f, native, baseline) && splitTransferSameAllocation(posting, a.Posting) && splitTransferAllParentLinesReserved(a.Posting, baseline)
}
