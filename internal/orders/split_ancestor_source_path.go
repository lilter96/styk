package orders

// AncestorSources returns the complete source preflight path. A retained
// reduction additionally checks its actual native self, as legacy permissions
// did. Native target checks still use Ancestors, never this source-only path.
func (p SplitAncestorSettlementPermission) AncestorSources() []Posting {
	if len(p.SourceAncestors) >= MaxSplitLineageDepth || len(p.Ancestors) > MaxSplitLineageDepth {
		return nil
	}
	result := make([]Posting, 0, MaxSplitLineageDepth)
	seen := make(map[string]bool, MaxSplitLineageDepth)
	for _, source := range p.SourceAncestors {
		if !seen[source.PostingNumber] {
			result = append(result, minimalSupersededPosting(source))
			seen[source.PostingNumber] = true
		}
	}
	for _, ancestor := range p.Ancestors {
		if !seen[ancestor.Posting.PostingNumber] && (len(p.SourceAncestors) == 0 || ancestor.Posting.PostingNumber == p.Source.PostingNumber) {
			result = append(result, minimalSupersededPosting(ancestor.Posting))
			seen[ancestor.Posting.PostingNumber] = true
		}
	}
	return result
}

// AncestorSourceMatches retains the existing source revision/allocation rules
// for both actual native frontiers and explicitly native-free intermediates.
func (p SplitAncestorSettlementPermission) AncestorSourceMatches(index int, source Posting) bool {
	path := p.AncestorSources()
	if index < 0 || index >= len(path) {
		return false
	}
	return (SplitAncestorNative{Posting: path[index]}).SourceMatches(source)
}

func (p SplitAncestorSettlementPermission) sourceAncestorPathValid() bool {
	if len(p.SourceAncestors) == 0 {
		return true
	}
	if len(p.SourceAncestors) >= MaxSplitLineageDepth {
		return false
	}
	seen := make(map[string]bool, len(p.SourceAncestors))
	previous := ""
	for _, source := range p.SourceAncestors {
		if !p.sourceAncestorSnapshotValid(source) || seen[source.PostingNumber] || source.PostingNumber == p.Source.PostingNumber || !ancestorParentConsistent(source, previous) {
			return false
		}
		seen[source.PostingNumber], previous = true, source.PostingNumber
	}
	return ancestorParentConsistent(p.Source, previous) && p.nativeSourcePathValid(seen)
}

func (p SplitAncestorSettlementPermission) sourceAncestorSnapshotValid(source Posting) bool {
	return source.Scope == p.Scope && source.CommercialOrderID == p.Source.CommercialOrderID && len(source.Lines) <= MaxNativeOrderPointPositions && len(source.ChildPostingNumbers) <= MaxSplitLineageHeads && lineagePostingTrustworthy(source)
}

func (p SplitAncestorSettlementPermission) nativeSourcePathValid(seen map[string]bool) bool {
	next := 0
	for index, ancestor := range p.Ancestors {
		number := ancestor.Posting.PostingNumber
		if number == p.Source.PostingNumber {
			return p.Mode == SplitAncestorReduce && index == len(p.Ancestors)-1 && splitTransferPostingEqual(ancestor.Posting, p.Source)
		}
		if !seen[number] {
			return false
		}
		for next < len(p.SourceAncestors) && p.SourceAncestors[next].PostingNumber != number {
			next++
		}
		if next == len(p.SourceAncestors) || !splitTransferPostingEqual(p.SourceAncestors[next], ancestor.Posting) {
			return false
		}
		next++
	}
	return true
}
