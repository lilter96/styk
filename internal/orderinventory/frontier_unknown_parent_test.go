package orderinventory

import (
	"strconv"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestConservedChildrenCannotHideUnknownParentEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(*Observation)
	}{
		{"missing status", func(o *Observation) { o.Posting.Status = orders.Field[string]{} }},
		{"null status", func(o *Observation) { o.Posting.Status = orders.Field[string]{Presence: orders.PresenceNull} }},
		{"native read failure", func(o *Observation) { o.NativeReadCode = "NATIVE_ORDER_NOT_FOUND" }},
		{"unknown native write", func(o *Observation) { o.Work.State = orders.StateUnknown }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := inventoryFixture()
			parent := input.Observations[0]
			parent.Posting.ChildPostingNumbers = []string{"child-1", "child-2"}
			tc.change(&parent)
			input.Observations = []Observation{parent}
			for index := 1; index <= 2; index++ {
				child := inventoryObservation(input.Now, "child-"+strconv.Itoa(index), strconv.Itoa(index))
				child.Posting.ParentPostingNumber = orders.Present(parent.Posting.PostingNumber)
				input.Observations = append(input.Observations, child)
			}
			if excluded, _ := reservationFrontier(input.Observations); excluded[observationKey(parent.Posting)] {
				t.Fatal("conserving child quantities hid unresolved parent evidence")
			}
			if guard := Assess(input); !guard.Blocked || len(guard.Obligations) != 3 {
				t.Fatalf("unresolved parent did not fence its affected pool: %+v", guard)
			}
		})
	}
}
