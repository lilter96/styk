package orders

import "time"

func forestScalarMetadata(f SplitForestFacts, previous, event SplitForestNative) bool {
	prior, err := ProjectionPositionBaseline(previous.Work, previous.Target)
	if err != nil || !splitScalarMetadataObserved(event.Work, event.Target, prior) {
		return false
	}
	posting, found := splitMetadataSourceAt(f, event.Source.PostingNumber, event.Attempt.CreatedAt)
	if !found || !splitTransferSameAllocation(posting, event.Source) {
		return false
	}
	c := event.Work.Command
	if p := c.SplitAncestorSettlement; p != nil {
		return p.Mode == SplitAncestorReduce && forestGrant(f, event, *p) && p.AllowsCommand(c)
	}
	if c.SplitSourceWorkID != "" || c.SplitTransfer != nil || c.SupersededSplitWorkID != "" {
		return false
	}
	if c.RetainedSplitWorkID == "" {
		return true // Exact unchanged metadata needs no reserve transfer grant.
	}
	step, found := forestLastSplit(f, event.Source.PostingNumber, event.Attempt.CreatedAt)
	if !found || step.Work.ID != c.RetainedSplitWorkID || step.Work.Command.Correlation != c.Correlation {
		return false
	}
	p := SplitRetainedReservePermission{Scope: f.Lineage.Scope, SplitWorkID: step.Work.ID, Before: step.Before, Correlation: c.Correlation}
	return p.ValidateWrite(event.Source, *c.CustomerOrder, prior) == nil
}

func splitMetadataSourceAt(f SplitForestFacts, number string, cut time.Time) (Posting, bool) {
	posting := forestPrefix(f, number, cut)
	if posting.PostingNumber != "" {
		return posting, true
	}
	if _, exists := forestLastSplit(f, number, cut); exists || number != f.Lineage.RootPosting {
		return Posting{}, false
	}
	root := forestRoot(f)
	return root.Before, root.Before.PostingNumber == number
}
