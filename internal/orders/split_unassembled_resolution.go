package orders

import (
	"reflect"
	"slices"
)

type UnassembledSplitResolution struct {
	Confirmed         bool
	PostingNumbers    []string
	Parent            *Posting
	ParentUnavailable bool
}

// SplitObservedForAction preserves the original assembly proof's status rules.
// Unassembled split proves allocation only, never successful packing/shipping.
func SplitObservedForAction(action Action, before Posting, packages []ActionPackage, numbers []string, children []Posting) bool {
	if action == ActionPostingSplit {
		return SplitObserved(before, packages, numbers, children)
	}
	return action == ActionPostingSplitUnassembled && splitAllocationObserved(before, packages, numbers, children, unassembledChildValid)
}

func unassembledChildValid(before, child Posting) bool {
	if child.Validate() != nil || child.Evidence.Origin != OriginOzon || child.Scope != before.Scope || child.CommercialOrderID != before.CommercialOrderID || !reflect.DeepEqual(before.WarehouseID, child.WarehouseID) || !reflect.DeepEqual(before.DeliveryMethodID, child.DeliveryMethodID) || !reflect.DeepEqual(before.Scheme, child.Scheme) {
		return false
	}
	if child.ParentPostingNumber.IsKnown() && *child.ParentPostingNumber.Value != before.PostingNumber {
		return false
	}
	if !child.Status.IsKnown() || child.Substatus.IsKnown() && *child.Substatus.Value == "ship_failed" {
		return false
	}
	if !slices.Contains([]string{"awaiting_packaging", "awaiting_approve", "awaiting_registration", "awaiting_deliver", "delivering", "driver_pickup", "delivered", "cancelled", SupersededSplitParentStatus, "acceptance_in_progress", "arbitration", "client_arbitration", "not_accepted"}, *child.Status.Value) {
		return false
	}
	for _, drift := range child.Drift {
		if drift.Code != "schema_unverified" && drift.Code != "unknown_key" && drift.Code != "unknown_related_posting_field" {
			return false
		}
	}
	return true
}

func ResolveUnassembledSplit(before Posting, work Work, reads []SplitReadback) (UnassembledSplitResolution, error) {
	var result UnassembledSplitResolution
	candidates, err := UnassembledSplitCandidates(work.Command, work.Receipt)
	if err != nil || work.Command.PostingAction == nil || before.Scope != work.Scope || before.PostingNumber != work.Command.PostingNumber || before.Evidence.Revision != work.Command.ExpectedSourceRevision {
		return result, ErrBlocked
	}
	observed, err := unassembledReadMap(work, candidates, reads)
	if err != nil {
		return result, err
	}
	if parent, found := observed[before.PostingNumber]; found {
		result.Parent = &parent
	} else {
		result.ParentUnavailable = slices.ContainsFunc(reads, func(read SplitReadback) bool {
			return read.PostingNumber == before.PostingNumber && read.Code == "SOURCE_UNAVAILABLE" && read.Posting == nil
		})
		if !result.ParentUnavailable {
			return result, nil
		}
	}
	return resolveUnassembledAllocation(before, work, observed, result), nil
}

func resolveUnassembledAllocation(before Posting, work Work, observed map[string]Posting, result UnassembledSplitResolution) UnassembledSplitResolution {
	allocations := make([]Posting, 0, len(observed))
	for _, declared := range *work.Receipt.UnassembledSplit.Postings.Value {
		number := *declared.PostingNumber.Value
		posting, found := observed[number]
		if !found || !unassembledResponseObserved(declared, posting) {
			return result
		}
		allocations = append(allocations, posting)
	}
	if result.Parent != nil && !slices.ContainsFunc(allocations, func(p Posting) bool { return p.PostingNumber == before.PostingNumber }) && (!result.Parent.Status.IsKnown() || *result.Parent.Status.Value != SupersededSplitParentStatus) {
		allocations = append(allocations, *result.Parent)
	}
	result.PostingNumbers = make([]string, len(allocations))
	for i, posting := range allocations {
		result.PostingNumbers[i] = posting.PostingNumber
	}
	result.Confirmed = SplitObservedForAction(work.Command.Action, before, work.Command.PostingAction.Parameters.Packages, result.PostingNumbers, allocations)
	if result.Confirmed {
		slices.Sort(result.PostingNumbers)
	}
	return result
}

func unassembledReadMap(work Work, candidates []string, reads []SplitReadback) (map[string]Posting, error) {
	result := make(map[string]Posting, len(reads))
	seen := make(map[string]bool, len(reads))
	for _, read := range reads {
		if seen[read.PostingNumber] || !slices.Contains(candidates, read.PostingNumber) {
			return nil, ErrConflict
		}
		seen[read.PostingNumber] = true
		if read.CheckedAt.IsZero() || read.CheckedAt.Before(work.Receipt.ObservedAt) {
			return nil, ErrStale
		}
		if read.Code != "OBSERVED" || read.Posting == nil {
			continue
		}
		posting := *read.Posting
		if posting.Validate() != nil || posting.Scope != work.Scope || posting.PostingNumber != read.PostingNumber || posting.CommercialOrderID != work.Command.CommercialOrderID || posting.Evidence.Origin != OriginOzon || read.CheckedAt.Before(work.Receipt.ObservedAt) {
			return nil, ErrStale
		}
		result[posting.PostingNumber] = posting
	}
	return result, nil
}

func unassembledResponseObserved(declared SplitResponsePosting, posting Posting) bool {
	if !declared.Products.IsKnown() || len(*declared.Products.Value) != len(posting.Lines) {
		return false
	}
	products := make(map[string]Decimal, len(posting.Lines))
	for _, line := range posting.Lines {
		if products[line.Identity.SKU] != "" {
			return false
		}
		products[line.Identity.SKU] = line.Quantity
	}
	for _, product := range *declared.Products.Value {
		if !ExactDecimalEqual(products[product.SKU], product.Quantity) {
			return false
		}
	}
	return true
}
