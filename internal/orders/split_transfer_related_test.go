package orders

import "testing"

func TestSplitTransferFreshSourceUsesAllocationRatherThanRetainedSiblingMetadata(t *testing.T) {
	t.Parallel()
	facts := splitTransferFixture(t)
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil {
		t.Fatal(err)
	}
	// A merged head may retain last-known assembly membership while a fresh
	// shortened detail supplies null. Neither is proof of ancestry or allocation.
	proof.ParentPosting.RelatedPostings = &RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{"assembly-peer"})}
	fresh := facts.Parent
	fresh.RelatedPostings = &RelatedPostingGroup{Presence: PresenceNull}
	if !proof.ParentSourceMatches(fresh) {
		t.Fatal("retained informational membership permanently blocked a fresh unchanged allocation")
	}
	fresh.Lines = append([]PostingLine(nil), fresh.Lines...)
	fresh.Lines[0].Quantity = "99"
	if proof.ParentSourceMatches(fresh) {
		t.Fatal("sibling metadata exception bypassed changed quantity")
	}
	fresh = facts.Parent
	fresh.RelatedPostings = &RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Field[[]string]{Presence: PresenceValue}}
	if proof.ParentSourceMatches(fresh) {
		t.Fatal("malformed related fact was ignored before validation")
	}
}
