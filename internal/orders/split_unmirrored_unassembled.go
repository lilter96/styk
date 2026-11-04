package orders

import "slices"

// NoParentNative is supplied only by scoped server-side database reconstruction:
// no native binding and no historical, pending or UNKNOWN parent mirror work.
// It is not a provider absence assertion and is never sufficient by itself.
type UnmirroredUnassembledSplitFacts struct {
	Split               Work
	Before, Parent      Posting
	Children            []Posting
	OriginalCorrelation Correlation
	NoParentNative      bool
}

func NewUnmirroredUnassembledSplitReserve(facts UnmirroredUnassembledSplitFacts, child Posting) (SplitReservePermission, error) {
	if !validUnmirroredUnassembledWork(facts) || !validUnmirroredUnassembledCurrent(facts) || !splitTransferChildReservable(child) {
		return SplitReservePermission{}, ErrBlocked
	}
	previous := facts.Split.Receipt.ObservedPostings
	index := slices.IndexFunc(previous, func(p Posting) bool { return p.PostingNumber == child.PostingNumber })
	current := slices.IndexFunc(facts.Children, func(p Posting) bool { return p.PostingNumber == child.PostingNumber })
	if index < 0 || current < 0 || !splitTransferPostingEqual(minimalSupersededPosting(facts.Children[current]), minimalSupersededPosting(child)) || !splitTransferSameAllocation(previous[index], child) {
		return SplitReservePermission{}, ErrBlocked
	}
	permission := SplitReservePermission{Scope: facts.Before.Scope, SplitWorkID: facts.Split.ID, ParentPostingNumber: facts.Before.PostingNumber, PostingNumber: child.PostingNumber}
	if !permission.Allows(child) {
		return SplitReservePermission{}, ErrBlocked
	}
	return permission, nil
}

func validUnmirroredUnassembledWork(f UnmirroredUnassembledSplitFacts) bool {
	w, b, c := f.Split, f.Before, f.Split.Command
	if !f.NoParentNative || !lineagePostingTrustworthy(b) || b.ParentPostingNumber.IsKnown() || len(b.ChildPostingNumbers) > 0 || !validID(w.ID) || w.Scope != b.Scope || w.State != StateReconciled || w.DispatchCount != 1 {
		return false
	}
	if c.Action != ActionPostingSplitUnassembled || c.Scope != b.Scope || c.PostingNumber != b.PostingNumber || c.CommercialOrderID != b.CommercialOrderID || c.ExpectedSourceRevision != b.Evidence.Revision || c.PostingAction == nil {
		return false
	}
	original := f.OriginalCorrelation
	if original.Validate() != nil || original.Scope != b.Scope || original.PostingNumber != b.PostingNumber || original.CustomerOrderID != "" || c.Correlation != original {
		return false
	}
	r := w.Receipt
	for _, posting := range r.ObservedPostings {
		if !splitTransferProductIdentities(b.Lines, posting.Lines) {
			return false
		}
	}
	return r.CommandID == c.ID && r.State == StateReconciled && unassembledAcknowledgedAllocation(w) && SplitObservedForAction(c.Action, b, c.PostingAction.Parameters.Packages, r.PostingNumbers, r.ObservedPostings)
}

func validUnmirroredUnassembledCurrent(f UnmirroredUnassembledSplitFacts) bool {
	r, b, p := f.Split.Receipt, f.Before, f.Parent
	if !lineagePostingTrustworthy(p) || p.PostingNumber != b.PostingNumber || p.ParentPostingNumber.IsKnown() || !sameFulfillmentTarget(b, p) {
		return false
	}
	retained := slices.IndexFunc(r.ObservedPostings, func(posting Posting) bool { return posting.PostingNumber == b.PostingNumber })
	if retained < 0 {
		if !knownLineageRetired(p) || p.Times.DeliveringAt.IsKnown() || p.Times.DeliveredAt.IsKnown() || reservationAfterShipment(p.Cancellation) || !splitTransferSameAllocation(b, p) {
			return false
		}
	} else if knownLineageRetired(p) || !splitTransferSameAllocation(r.ObservedPostings[retained], p) {
		return false
	}
	expectedChildren := slices.DeleteFunc(slices.Clone(r.PostingNumbers), func(number string) bool { return number == b.PostingNumber })
	if len(p.ChildPostingNumbers) > 0 && !sameSupersededNumbers(p.ChildPostingNumbers, expectedChildren) {
		return false
	}
	if !SplitObservedForAction(f.Split.Command.Action, b, f.Split.Command.PostingAction.Parameters.Packages, r.PostingNumbers, f.Children) {
		return false
	}
	for _, child := range f.Children {
		index := slices.IndexFunc(r.ObservedPostings, func(old Posting) bool { return old.PostingNumber == child.PostingNumber })
		if index < 0 || len(child.ChildPostingNumbers) > 0 && child.PostingNumber != b.PostingNumber || !lineagePostingTrustworthy(child) || !splitTransferSameAllocation(r.ObservedPostings[index], child) {
			return false
		}
	}
	return true
}
