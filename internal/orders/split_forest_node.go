package orders

func forestNodeAdmission(f SplitForestFacts, source Posting, step SplitLineageStep) (SplitAncestorSettlementPermission, error) {
	if source.PostingNumber == f.Lineage.RootPosting || !forestNodeHistoryEmpty(f, source.PostingNumber) || !forestNodeUnmirrored(f, source.PostingNumber, f.CheckedAt, "") {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	chain := forestChain(f, source.PostingNumber)
	if len(chain) < 2 {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	p := SplitAncestorSettlementPermission{Scope: f.Lineage.Scope, Mode: SplitAncestorNodeAdmit, RootSplitWorkID: forestRoot(f).Work.ID, SplitWorkID: step.Work.ID, Before: minimalSupersededPosting(step.Before), Source: minimalSupersededPosting(source), Node: minimalSupersededPosting(source)}
	p.Ancestors = make([]SplitAncestorNative, 0, len(chain)-1)
	for _, number := range chain[:len(chain)-1] {
		native, found := forestPrevious(f, number, f.CheckedAt, "")
		posting := forestHead(f, number)
		baseline, err := ProjectionPositionBaseline(native.Work, native.Target)
		if !found || err != nil || !forestInventoryAuthority(f, native, baseline) || !splitTransferAllParentLinesReserved(posting, baseline) {
			return p, ErrBlocked
		}
		p.Ancestors = append(p.Ancestors, SplitAncestorNative{Posting: minimalSupersededPosting(posting), WorkID: native.Work.ID, Target: native.Target})
		p.Baseline, p.NodeOriginWorkID = baseline, baseline.WorkID
	}
	if p.Validate() != nil {
		return p, ErrBlocked
	}
	return freezeAncestorSettlement(p)
}

func forestNodeHistoryEmpty(f SplitForestFacts, number string) bool {
	found := false
	for _, history := range f.NativeHistory {
		if history.PostingNumber != number {
			continue
		}
		if found || history.Scope != f.Lineage.Scope || history.BindingIDs == nil || history.MirrorWorkIDs == nil || len(history.BindingIDs) != 0 || len(history.MirrorWorkIDs) != 0 {
			return false
		}
		found = true
	}
	return found
}

func (p SplitAncestorSettlementPermission) validateNodeAdmission() error {
	if p.Scope.Validate() != nil || !validID(p.SplitWorkID) || !validID(p.RootSplitWorkID) || p.SplitWorkID == p.RootSplitWorkID || !validID(p.NodeOriginWorkID) || p.NodeReductionWorkID != "" || len(p.Ancestors) == 0 || len(p.Ancestors) > MaxSplitLineageDepth {
		return ErrBlocked
	}
	if p.Baseline.Validate() != nil || p.Baseline.Scope != p.Scope || p.Baseline.RecoveryID != "" || p.Baseline.EvidenceClass == "OPERATOR_ATTESTED" || p.Before.Scope != p.Scope || p.Node.Scope != p.Scope || p.Source.Scope != p.Scope || !lineagePostingTrustworthy(p.Before) || !lineagePostingTrustworthy(p.Node) || !lineagePostingTrustworthy(p.Source) {
		return ErrBlocked
	}
	if p.Before.PostingNumber != p.Node.PostingNumber || !splitTransferPostingEqual(p.Source, p.Node) || !splitTransferChildReservable(p.Source) || !forestNodeSourceReduced(p.Before, p.Node) {
		return ErrBlocked
	}
	seen, nativeIDs := make(map[string]bool), make(map[string]bool)
	for _, a := range p.Ancestors {
		if a.Posting.PostingNumber == p.Source.PostingNumber || seen[a.Posting.PostingNumber] || nativeIDs[a.Target.Correlation.CustomerOrderID] || !validSplitAncestorNative(a, p.Scope) {
			return ErrBlocked
		}
		seen[a.Posting.PostingNumber], nativeIDs[a.Target.Correlation.CustomerOrderID] = true, true
	}
	last := p.Ancestors[len(p.Ancestors)-1]
	if p.NodeOriginWorkID != p.Baseline.WorkID || p.Baseline.Correlation != last.Target.Correlation || !splitMetadataAuthorityTarget(p.Baseline, last.Target) || !ancestorParentConsistent(p.Node, last.Posting.PostingNumber) {
		return ErrBlocked
	}
	return nil
}

func forestNodeSourceReduced(before, after Posting) bool {
	original, invalid := reservationLineQuantities(before.Lines)
	if invalid.Code != "" || !sameFulfillmentTarget(before, after) || !splitTransferProductIdentities(before.Lines, after.Lines) {
		return false
	}
	current, invalid := reservationLineQuantities(after.Lines)
	if invalid.Code != "" || len(current) == 0 {
		return false
	}
	for key, quantity := range current {
		previous, found := original[key]
		if !found || !nonnegativeNotGreater(quantity, previous) {
			return false
		}
	}
	return true
}
