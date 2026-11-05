package orders

func forestCurrentPermission(f SplitForestFacts) (SplitAncestorSettlementPermission, error) {
	selected := forestHead(f, f.Lineage.SelectedPosting)
	if native, found := forestPrevious(f, selected.PostingNumber, f.CheckedAt, ""); found {
		step, exists := forestNodeGeneration(f, selected.PostingNumber, f.CheckedAt)
		if !exists {
			return SplitAncestorSettlementPermission{}, ErrBlocked
		}
		return forestOwnedPermission(f, selected, selected.PostingNumber, true, native, step)
	}
	owner := selected.PostingNumber
	_, node := forestLastSplit(f, owner, f.CheckedAt)
	if !node {
		birth, found := forestBirth(f, owner)
		if !found {
			return SplitAncestorSettlementPermission{}, ErrBlocked
		}
		owner = birth.Before.PostingNumber
	}
	native, found := forestPrevious(f, owner, f.CheckedAt, "")
	step, split := forestLastSplit(f, owner, f.CheckedAt)
	if !split {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	if !found {
		generation, exists := forestNodeGeneration(f, selected.PostingNumber, f.CheckedAt)
		if !exists {
			return SplitAncestorSettlementPermission{}, ErrBlocked
		}
		return forestNodeAdmission(f, selected, generation)
	}
	return forestOwnedPermission(f, selected, owner, node, native, step)
}

func forestOwnedPermission(f SplitForestFacts, selected Posting, owner string, node bool, native SplitForestNative, step SplitLineageStep) (SplitAncestorSettlementPermission, error) {
	baseline, err := ProjectionPositionBaseline(native.Work, native.Target)
	if err != nil || !forestInventoryAuthority(f, native, baseline) {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	p := SplitAncestorSettlementPermission{Scope: f.Lineage.Scope, Mode: SplitAncestorReduce, RootSplitWorkID: forestRoot(f).Work.ID, SplitWorkID: step.Work.ID, NodeOriginWorkID: baseline.WorkID, Before: minimalSupersededPosting(step.Before), Source: minimalSupersededPosting(forestHead(f, owner)), Node: minimalSupersededPosting(forestHead(f, owner)), Baseline: baseline}
	if err = forestCurrentAncestors(f, owner, &p); err != nil {
		return p, err
	}
	if !node {
		if !splitTransferAllParentLinesReserved(p.Node, baseline) || !splitTransferChildReservable(selected) || !ancestorParentConsistent(selected, owner) {
			return p, ErrBlocked
		}
		p.Mode, p.Source, p.NodeReductionWorkID = SplitAncestorAdmit, minimalSupersededPosting(selected), baseline.WorkID
	}
	if p.Validate() != nil {
		return p, ErrBlocked
	}
	return freezeAncestorSettlement(p)
}

func forestCurrentAncestors(f SplitForestFacts, owner string, p *SplitAncestorSettlementPermission) error {
	chain := forestChain(f, owner)
	if len(chain) == 0 {
		return ErrBlocked
	}
	p.Ancestors = make([]SplitAncestorNative, 0, len(chain))
	missing := false
	for _, number := range chain {
		native, found := forestPrevious(f, number, f.CheckedAt, "")
		if !found {
			if !forestCurrentNativeFree(f, number) {
				return ErrBlocked
			}
			missing = true
			continue
		}
		p.Ancestors = append(p.Ancestors, SplitAncestorNative{Posting: minimalSupersededPosting(forestHead(f, number)), WorkID: native.Work.ID, Target: native.Target})
	}
	if missing {
		p.SourceAncestors = forestCurrentSourcePath(f, f.Lineage.SelectedPosting)
		if !forestCurrentNativeAncestorsSettled(f, owner, p.Ancestors) {
			return ErrBlocked
		}
	}
	return nil
}

func forestCurrentNativeAncestorsSettled(f SplitForestFacts, owner string, ancestors []SplitAncestorNative) bool {
	for _, ancestor := range ancestors {
		if ancestor.Posting.PostingNumber == owner {
			continue // This owner's partial frontier may authorize its own reduction.
		}
		native, found := forestPrevious(f, ancestor.Posting.PostingNumber, f.CheckedAt, "")
		baseline, err := ProjectionPositionBaseline(native.Work, native.Target)
		if !found || err != nil || !forestInventoryAuthority(f, native, baseline) || !splitTransferAllParentLinesReserved(ancestor.Posting, baseline) {
			return false
		}
	}
	return true
}
