package orders

import "time"

// The complete immutable split prefix proves every source-only intermediate.
// Keep each stored source's reserve/status semantics instead of refreshing it
// from the current head when replaying an earlier native event.
func forestSourcePathAt(f SplitForestFacts, p SplitAncestorSettlementPermission, cut time.Time) bool {
	if len(p.SourceAncestors) == 0 {
		return true
	}
	chain := forestChain(f, p.Source.PostingNumber)
	if len(chain) < 2 || len(p.SourceAncestors) != len(chain)-1 {
		return false
	}
	for i, source := range p.SourceAncestors {
		if source.PostingNumber != chain[i] || !splitTransferSameAllocation(forestPrefix(f, chain[i], cut), source) {
			return false
		}
	}
	return true
}
