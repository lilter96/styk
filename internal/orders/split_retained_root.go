package orders

// NewRetainedRootSplitReserve seals the exact last split of a completely
// native-free, same-root spine before its first native creation. NativeHistory
// is a complete scoped database absence witness, including pending/UNKNOWN work;
// an exact still-unsent fenced self may be excluded only by the store. The
// returned split ID must persist in the command and be reconstructed at dispatch.
func NewRetainedRootSplitReserve(f NestedSplitSettlementFacts, root Posting) (SplitReservePermission, error) {
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil || len(graph.path) < 2 || len(graph.path) != len(f.Lineage.Splits) || f.CheckedAt.IsZero() || f.Lineage.SelectedPosting != f.Lineage.RootPosting || root.PostingNumber != f.Lineage.RootPosting || !graphCurrentChild(graph, root) {
		return SplitReservePermission{}, ErrBlocked
	}
	if !nestedSettlementAttempts(f, graph) || !nestedSettlementNatives(f) || !nestedSettlementSources(f) || !retainedRootSpine(f.Lineage) {
		return SplitReservePermission{}, ErrBlocked
	}
	selected := graph.facts.Splits[graph.selectedStep()]
	if selected.Work.ID != f.SelectedSplitWorkID || selected.Before.PostingNumber != root.PostingNumber {
		return SplitReservePermission{}, ErrBlocked
	}
	permission := SplitReservePermission{RetainedRoot: true, Scope: f.Lineage.Scope, SplitWorkID: selected.Work.ID, ParentPostingNumber: root.PostingNumber, PostingNumber: root.PostingNumber}
	if !permission.Allows(root) || !splitTransferChildReservable(root) {
		return SplitReservePermission{}, ErrBlocked
	}
	return permission, nil
}

func retainedRootSpine(f SplitLineageFacts) bool {
	if len(f.Splits) < 2 {
		return false
	}
	for _, step := range f.Splits {
		if step.Before.PostingNumber != f.RootPosting || ancestorNodeOutput(step.Work, f.RootPosting).PostingNumber != f.RootPosting {
			return false
		}
	}
	return true
}
