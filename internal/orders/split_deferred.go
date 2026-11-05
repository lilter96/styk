package orders

// SplitDeferredNative carries the immutable approved source of an actual native
// operation. Source is historical evidence, never a fabricated current head.
type SplitDeferredNative struct {
	Work   Work
	Target TargetObservation
	Source Posting
}

// SplitDeferredSettlementFacts covers a complete same-root retained spine.
// Reductions includes every native change, not merely its latest successful row.
type SplitDeferredSettlementFacts struct {
	Spine      []SplitLineageStep
	Origin     SplitDeferredNative
	Reductions []SplitDeferredNative
	Current    Posting
}

// DeferredChildReservable uses the ordinary reserve eligibility rules. Deferred
// proof must not take over cancellation or superseded-parent workflows.
func DeferredChildReservable(posting Posting) bool {
	return splitTransferChildReservable(posting)
}

func newDeferredSettlement(f SplitAncestorSettlementFacts) (SplitAncestorSettlementPermission, error) {
	if !deferredStructure(f) || !deferredClocks(f) {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	d := f.Deferred
	frontier := d.Origin
	for _, reduction := range d.Reductions {
		if !deferredReduction(f, frontier, reduction) {
			return SplitAncestorSettlementPermission{}, ErrBlocked
		}
		frontier = reduction
	}
	p, err := deferredPermission(f, frontier, d.Current, d.Spine[len(d.Spine)-1])
	if err != nil {
		return p, ErrBlocked
	}
	if f.Selected.PostingNumber != d.Current.PostingNumber {
		// A first native create after the last split may already own exactly
		// this allocation. Preserve that actual work ID; do not invent a release.
		if !splitTransferAllParentLinesReserved(d.Current, p.Baseline) || !splitTransferChildReservable(f.Selected) || !ancestorParentConsistent(f.Selected, d.Current.PostingNumber) {
			return p, ErrBlocked
		}
		p.Mode, p.Source, p.NodeReductionWorkID = SplitAncestorAdmit, minimalSupersededPosting(f.Selected), frontier.Work.ID
	}
	if p.Validate() != nil {
		return p, ErrBlocked
	}
	return freezeAncestorSettlement(p)
}

func deferredPermission(f SplitAncestorSettlementFacts, prior SplitDeferredNative, node Posting, split SplitLineageStep) (SplitAncestorSettlementPermission, error) {
	baseline, err := ProjectionPositionBaseline(prior.Work, prior.Target)
	if err != nil {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	d := f.Deferred
	return SplitAncestorSettlementPermission{
		Scope: f.Lineage.Scope, Mode: SplitAncestorReduce,
		RootSplitWorkID: d.Spine[0].Work.ID, SplitWorkID: split.Work.ID,
		NodeOriginWorkID: prior.Work.ID, Before: minimalSupersededPosting(d.Spine[0].Before),
		Source: minimalSupersededPosting(node), Node: minimalSupersededPosting(node), Baseline: baseline,
		Ancestors: []SplitAncestorNative{{Posting: minimalSupersededPosting(node), WorkID: prior.Work.ID, Target: prior.Target}},
	}, nil
}
