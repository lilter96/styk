package orders

import (
	"reflect"
	"slices"
	"time"
)

func ancestorSettlementHeadsFresh(heads []Posting, checkedAt time.Time) bool {
	if checkedAt.IsZero() {
		return false
	}
	for _, head := range heads {
		checked := head.Evidence.CheckedAt
		if checked.IsZero() {
			checked = head.Evidence.ObservedAt
		}
		if checked.Before(checkedAt.Add(-30*time.Second)) || checked.After(checkedAt.Add(5*time.Second)) {
			return false
		}
	}
	return true
}

func ancestorSettlementNode(f SplitAncestorSettlementFacts) bool {
	split, before := f.NodeSplit.Work, f.NodeSplit.Before
	if split.Command.Action != ActionPostingSplitUnassembled || !unassembledAcknowledgedAllocation(split) || split.Command.Correlation != f.NodeOriginTarget.Correlation || split.Command.ExpectedSourceRevision != before.Evidence.Revision || !ancestorParentConsistent(before, f.RootTransfer.Parent.PostingNumber) {
		return false
	}
	if len(split.Receipt.PostingNumbers) != 2 || !slices.Contains(split.Receipt.PostingNumbers, before.PostingNumber) || f.CurrentNode.PostingNumber != before.PostingNumber || !splitTransferSameAllocation(f.CurrentNode, ancestorNodeOutput(split, before.PostingNumber)) {
		return false
	}
	for _, posting := range split.Receipt.ObservedPostings {
		if !splitTransferProductIdentities(before.Lines, posting.Lines) {
			return false
		}
	}
	return true
}

// A provider parent field is optional. Local ancestry instead comes from the
// exact immutable acknowledged split generations, not related_postings or a
// client-supplied parent assertion. Known contradictory provider facts still fail.
func ancestorSettlementLineage(f SplitAncestorSettlementFacts, proof SplitLineageProof) bool {
	if !slices.Equal(proof.PathWorkIDs(), []string{f.RootTransfer.Split.ID, f.NodeSplit.Work.ID}) {
		return false
	}
	root, node := false, false
	for _, step := range f.Lineage.Splits {
		if step.Work.ID == f.RootTransfer.Split.ID {
			root = reflect.DeepEqual(step.Work, f.RootTransfer.Split) && splitTransferPostingEqual(step.Before, f.RootTransfer.Before)
		}
		if step.Work.ID == f.NodeSplit.Work.ID {
			node = reflect.DeepEqual(step.Work, f.NodeSplit.Work) && splitTransferPostingEqual(step.Before, f.NodeSplit.Before)
		}
	}
	output := ancestorNodeOutput(f.NodeSplit.Work, f.Selected.PostingNumber)
	return root && node && splitTransferSameAllocation(output, f.Selected) && (f.Selected.PostingNumber == f.CurrentNode.PostingNumber || ancestorParentConsistent(f.Selected, f.CurrentNode.PostingNumber))
}

func ancestorParentConsistent(posting Posting, expected string) bool {
	return posting.ParentPostingNumber.Validate() == nil && (!posting.ParentPostingNumber.IsKnown() || *posting.ParentPostingNumber.Value == expected)
}

func validSplitAncestorNative(a SplitAncestorNative, scope Scope) bool {
	if a.Posting.Scope != scope || a.Posting.Validate() != nil || !validID(a.WorkID) || a.Target.Scope != scope || !a.Target.Complete || !validID(a.Target.Revision) || a.Target.ObservedAt.IsZero() || !nativeDocumentPresent(a.Target.SourceDeleted) {
		return false
	}
	correlation := a.Target.Correlation
	return correlation.Validate() == nil && correlation.Scope == scope && correlation.PostingNumber == a.Posting.PostingNumber && correlation.CustomerOrderID != "" && a.Target.Fields.Applicable.IsKnown() && *a.Target.Fields.Applicable.Value
}

func (p SplitAncestorSettlementPermission) commandCorrelationMatches(c Command) bool {
	if c.Correlation.Validate() != nil || c.Correlation.Scope != p.Scope || c.Correlation.PostingNumber != p.Source.PostingNumber {
		return false
	}
	if p.Mode == SplitAncestorReduce {
		return c.Correlation == p.Baseline.Correlation
	}
	for _, ancestor := range p.Ancestors {
		if c.Correlation.CustomerOrderID != "" && c.Correlation.CustomerOrderID == ancestor.Target.Correlation.CustomerOrderID {
			return false
		}
	}
	return true
}
