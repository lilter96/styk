package orders

import "slices"

// Reconciled work is loaded through the scoped durable-work reader. This binds
// its historical allocation identities and quantities to the original ACK; it
// does not grant retirement or infer a native reserve reduction.
func unassembledAcknowledgedAllocation(work Work) bool {
	candidates, err := UnassembledSplitCandidates(work.Command, work.Receipt)
	if err != nil || len(work.Receipt.PostingNumbers) != len(work.Receipt.ObservedPostings) {
		return false
	}
	for _, number := range work.Receipt.PostingNumbers {
		if !slices.Contains(candidates, number) {
			return false
		}
	}
	for _, declared := range *work.Receipt.UnassembledSplit.Postings.Value {
		number := *declared.PostingNumber.Value
		index := slices.IndexFunc(work.Receipt.ObservedPostings, func(p Posting) bool { return p.PostingNumber == number })
		if index < 0 || !unassembledResponseObserved(declared, work.Receipt.ObservedPostings[index]) {
			return false
		}
	}
	return true
}
