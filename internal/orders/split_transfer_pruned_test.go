package orders

import (
	"slices"
	"testing"
)

func TestSplitTransferPrunedProofMatchesRichUnchangedAllocation(t *testing.T) {
	t.Parallel()
	facts := splitTransferFixture(t)
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil {
		t.Fatal(err)
	}
	richParent, richChild := richSplitTransferPosting(facts.Parent), richSplitTransferPosting(facts.Child)
	proof.ParentPosting, proof.ChildPosting = minimalSupersededPosting(facts.Parent), minimalSupersededPosting(facts.Child)
	if !proof.ParentSourceMatches(richParent) || !proof.Allows(richChild) {
		t.Fatal("pruned reserve proof rejected unchanged rich provider detail")
	}
	richParent.Lines[0].Quantity = "999"
	richChild.Lines[0].ProductID = Present("changed-product")
	if proof.ParentSourceMatches(richParent) || proof.Allows(richChild) {
		t.Fatal("metadata normalization ignored changed allocation identity")
	}
}

func richSplitTransferPosting(p Posting) Posting {
	p.Lines = slices.Clone(p.Lines)
	p.OrderNumber = Present("display-number")
	p.Lines[0].Name = Present("display-name")
	p.Financial = FinancialFacts{Presence: PresenceValue, OrderTotalAmount: Present(Decimal("25.99"))}
	p.Analytics = AnalyticsFacts{Presence: PresenceValue, Premium: Present(true)}
	p.Delivery = DeliveryFacts{Presence: PresenceValue, IntegrationType: Present("non_integrated")}
	p.AvailableActions = []string{"split"}
	p.Requirements = &PackingRequirements{Presence: PresenceEmpty}
	return p
}
