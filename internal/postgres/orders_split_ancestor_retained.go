package postgres

import "github.com/lilter96/styk/internal/orders"

func ownedPathRepeats(path []orders.SplitLineageStep) bool {
	seen := make(map[string]bool, len(path))
	for _, step := range path {
		if seen[step.Before.PostingNumber] {
			return true
		}
		seen[step.Before.PostingNumber] = true
	}
	return false
}

type retainedNativeRows struct {
	origins    map[string]ownedAncestorNativeRow
	reductions map[string]ownedAncestorNativeRow
}

// Every selected native row must be consumed by the proved generation history.
// Neither a latest-row shortcut nor an omitted UNKNOWN can establish ownership.
func partitionRetainedNativeRows(rows []ownedAncestorNativeRow, path []orders.SplitLineageStep) (retainedNativeRows, bool) {
	r := retainedNativeRows{origins: make(map[string]ownedAncestorNativeRow), reductions: make(map[string]ownedAncestorNativeRow)}
	steps, postings := make(map[string]string, len(path)), make(map[string]bool, len(path))
	for _, step := range path {
		steps[step.Work.ID], postings[step.Before.PostingNumber] = step.Before.PostingNumber, true
	}
	for _, row := range rows {
		w := row.work
		if w.State != orders.StateReconciled || w.DispatchCount != 1 || !postings[w.Command.PostingNumber] {
			return r, false
		}
		if w.Command.Action == orders.ActionMirrorCreate {
			if r.origins[w.Command.PostingNumber].work.ID != "" {
				return r, false
			}
			r.origins[w.Command.PostingNumber] = row
			continue
		}
		id := retainedNativeSplitID(w)
		if id == "" || steps[id] != w.Command.PostingNumber || r.reductions[id].work.ID != "" {
			return r, false
		}
		r.reductions[id] = row
	}
	return r, true
}

func retainedNativeSplitID(w orders.Work) string {
	if w.Command.Action != orders.ActionMirrorPositionUpdate {
		return ""
	}
	if w.Command.SplitAncestorSettlement == nil {
		return w.Command.RetainedSplitWorkID
	}
	if w.Command.RetainedSplitWorkID != "" || w.Command.SplitAncestorSettlement.Mode != orders.SplitAncestorReduce {
		return ""
	}
	return w.Command.SplitAncestorSettlement.SplitWorkID
}

func assignRetainedOwnedHistory(f *orders.SplitAncestorSettlementFacts, rows []ownedAncestorNativeRow, path []orders.SplitLineageStep) bool {
	partition, ok := partitionRetainedNativeRows(rows, path)
	if !ok {
		return false
	}
	previous := make(map[string]ownedAncestorNativeRow, len(path))
	seen, used := make(map[string]bool, len(path)), make(map[string]bool, len(rows))
	f.RetainedHistory = make([]orders.SplitRetainedGeneration, 0, len(path))
	for _, split := range path {
		number := split.Before.PostingNumber
		origin := partition.origins[number]
		if seen[number] {
			origin = previous[number]
		}
		g, valid := retainedOwnedGeneration(split, origin, partition.reductions[split.Work.ID])
		if !valid {
			return false
		}
		f.RetainedHistory = append(f.RetainedHistory, g)
		used[origin.work.ID], seen[number] = true, true
		delete(previous, number)
		if g.Reduction != nil {
			previous[number] = partition.reductions[split.Work.ID]
			used[g.Reduction.ID] = true
		}
	}
	if len(used) != len(rows) {
		return false
	}
	assignRetainedOwnedEndpoints(f)
	f.HistoryUnambiguous = true
	return true
}

func retainedOwnedGeneration(split orders.SplitLineageStep, origin, reduction ownedAncestorNativeRow) (orders.SplitRetainedGeneration, bool) {
	g := orders.SplitRetainedGeneration{Split: split, Origin: origin.work, OriginTarget: origin.target}
	if origin.work.ID == "" {
		return g, false
	}
	for _, output := range split.Work.Receipt.ObservedPostings {
		if output.PostingNumber == split.Before.PostingNumber {
			g.Retained = output
		}
	}
	if g.Retained.PostingNumber != split.Before.PostingNumber {
		return g, false
	}
	if reduction.work.ID != "" {
		work, target := reduction.work, reduction.target
		g.Reduction, g.ReductionTarget = &work, &target
	}
	return g, true
}

func assignRetainedOwnedEndpoints(f *orders.SplitAncestorSettlementFacts) {
	root, node := f.RetainedHistory[0], f.RetainedHistory[len(f.RetainedHistory)-1]
	f.RootOrigin, f.RootOriginTarget = root.Origin, root.OriginTarget
	f.RootTransfer.Split, f.RootTransfer.Before, f.RootTransfer.OriginalCorrelation = root.Split.Work, root.Split.Before, root.Split.Work.Command.Correlation
	f.RootTransfer.Parent = root.Retained
	for _, output := range root.Split.Work.Receipt.ObservedPostings {
		if output.PostingNumber != root.Split.Before.PostingNumber {
			f.RootTransfer.Child = output
			break
		}
	}
	if root.Reduction != nil && root.ReductionTarget != nil {
		f.RootTransfer.ParentRelease, f.RootTransfer.ParentTarget = *root.Reduction, *root.ReductionTarget
	}
	f.NodeOrigin, f.NodeOriginTarget = node.Origin, node.OriginTarget
	f.NodeReduction, f.NodeReductionTarget = node.Reduction, node.ReductionTarget
}

func retainedOwnedMappingsCurrent(history []orders.SplitRetainedGeneration, current *orders.ReviewContext) bool {
	for _, generation := range history {
		works := []orders.Work{generation.Origin}
		if generation.Reduction != nil {
			works = append(works, *generation.Reduction)
		}
		for _, w := range works {
			if w.Command.CustomerOrder == nil || !retainedAdmissionMappingsCurrent(orders.RetainedSplitAdmissionFacts{Admission: w}, current) {
				return false
			}
		}
	}
	return true
}
