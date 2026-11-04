package orders

import (
	"reflect"
	"testing"
)

func TestSplitLineageCurrentMatchesOriginalRichSource(t *testing.T) {
	t.Parallel()
	facts := lineageFixture(t)
	current := &facts.CurrentHeads[1]
	current.OrderNumber = Present("commercial-display-number")
	current.Lines[0].Name = Present("merchant source name")
	current.Financial = FinancialFacts{Presence: PresenceValue, Currency: Present("RUB")}
	current.Analytics = AnalyticsFacts{Presence: PresenceValue, City: Present("synthetic city")}
	current.Delivery = DeliveryFacts{Presence: PresenceValue, TrackingNumber: Present("synthetic-tracking")}
	current.AvailableActions = []string{"ship"}
	proof, err := NewSplitLineage(facts)
	if err != nil {
		t.Fatal(err)
	}
	if !proof.CurrentMatches(*current) {
		t.Fatal("unchanged rich source fails sanitized structural comparison")
	}
	if proof.selected.Lines[0].Name.IsKnown() || proof.selected.Delivery.TrackingNumber.IsKnown() || proof.selected.Analytics.City.IsKnown() || proof.selected.Financial.Currency.IsKnown() {
		t.Fatal("structural witness retained rich metadata")
	}
	current.Evidence.Revision = "changed-source"
	if proof.CurrentMatches(*current) {
		t.Fatal("new source revision accepted")
	}
}

func TestSplitLineagePrivateCacheMutationRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		alter func(*SplitLineageProof)
	}{
		{"path", func(p *SplitLineageProof) { p.path[0] = "forged-work" }},
		{"frontier", func(p *SplitLineageProof) { p.frontier = p.frontier[:1] }},
		{"selected", func(p *SplitLineageProof) { p.selected.PostingNumber = "forged-posting" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proof, err := NewSplitLineage(lineageFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			tc.alter(&proof)
			if proof.Validate() == nil {
				t.Fatal("cached state not bound to frozen witness")
			}
		})
	}
}

func TestSplitLineagePublicReturnsAreDeepCopies(t *testing.T) {
	t.Parallel()
	facts := lineageFixture(t)
	proof, err := NewSplitLineage(facts)
	if err != nil {
		t.Fatal(err)
	}
	original := proof.Frontier()
	returned := proof.Frontier()
	*returned[0].Status.Value = "changed"
	returned[0].Lines[0].Quantity = "999"
	path := proof.PathWorkIDs()
	path[0] = "changed"
	if !reflect.DeepEqual(proof.Frontier(), original) || proof.PathWorkIDs()[0] == "changed" {
		t.Fatal("public return changed witness")
	}
}
