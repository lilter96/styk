package orders

func forestCurrentPermission(f SplitForestFacts) (SplitAncestorSettlementPermission, error) {
	selected := forestHead(f, f.Lineage.SelectedPosting)
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
	if !found || !split {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	baseline, err := ProjectionPositionBaseline(native.Work, native.Target)
	if err != nil {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	p := SplitAncestorSettlementPermission{Scope: f.Lineage.Scope, Mode: SplitAncestorReduce, RootSplitWorkID: forestRoot(f).Work.ID, SplitWorkID: step.Work.ID, NodeOriginWorkID: native.Work.ID, Before: minimalSupersededPosting(step.Before), Source: minimalSupersededPosting(forestHead(f, owner)), Node: minimalSupersededPosting(forestHead(f, owner)), Baseline: baseline}
	if err = forestCurrentAncestors(f, owner, &p); err != nil {
		return p, err
	}
	if !node {
		if !splitTransferAllParentLinesReserved(p.Node, baseline) || !splitTransferChildReservable(selected) || !ancestorParentConsistent(selected, owner) {
			return p, ErrBlocked
		}
		p.Mode, p.Source, p.NodeReductionWorkID = SplitAncestorAdmit, minimalSupersededPosting(selected), native.Work.ID
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
	for _, number := range chain {
		native, found := forestPrevious(f, number, f.CheckedAt, "")
		if !found {
			return ErrBlocked
		}
		p.Ancestors = append(p.Ancestors, SplitAncestorNative{Posting: minimalSupersededPosting(forestHead(f, number)), WorkID: native.Work.ID, Target: native.Target})
	}
	return nil
}
