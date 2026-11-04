package orders

import (
	"slices"
	"strconv"
	"testing"
	"time"
)

func unassembledCandidateFixture() (Command, Receipt) {
	command := Command{ID: "intent", Action: ActionPostingSplitUnassembled, PostingNumber: "original"}
	receipt := Receipt{CommandID: command.ID, State: StateAcknowledged, HTTPStatus: 200, ObservedAt: time.Unix(100, 0).UTC(),
		UnassembledSplit: &UnassembledSplitAck{Postings: Present([]SplitResponsePosting{
			{PostingNumber: Present("child"), Products: Present([]SplitResponseProduct{{SKU: "9007199254740993", Quantity: "1"}})},
		})}}
	return command, receipt
}

func TestUnassembledCandidatesPreserveOptionalOriginalAndExactIDs(t *testing.T) {
	t.Parallel()
	for index, parent := range []Field[SplitResponsePosting]{{}, Present(SplitResponsePosting{}), Present(SplitResponsePosting{PostingNumber: Present("original")})} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			command, receipt := unassembledCandidateFixture()
			receipt.UnassembledSplit.Parent = parent
			numbers, err := UnassembledSplitCandidates(command, receipt)
			if err != nil || !slices.Equal(numbers, []string{"child", "original"}) || len(receipt.PostingNumbers) != 0 {
				t.Fatal("a read candidate was lost or represented as confirmed allocation", err)
			}
		})
	}
}

func TestUnassembledCandidatesRejectUnboundOrIncompleteFacts(t *testing.T) {
	t.Parallel()
	for index, change := range []func(*Command, *Receipt){
		func(c *Command, _ *Receipt) { c.Action = ActionPostingSplit },
		func(_ *Command, r *Receipt) { r.HTTPStatus = 409 },
		func(_ *Command, r *Receipt) { r.CommandID = "other" },
		func(_ *Command, r *Receipt) {
			r.UnassembledSplit.Parent = Present(SplitResponsePosting{PostingNumber: Present("other")})
		},
		func(_ *Command, r *Receipt) {
			r.UnassembledSplit.Parent = Field[SplitResponsePosting]{Presence: PresenceNull}
		},
		func(_ *Command, r *Receipt) { r.UnassembledSplit.Postings = Present([]SplitResponsePosting{}) },
		func(_ *Command, r *Receipt) {
			(*r.UnassembledSplit.Postings.Value)[0].Products = Field[[]SplitResponseProduct]{}
		},
		func(_ *Command, r *Receipt) {
			(*(*r.UnassembledSplit.Postings.Value)[0].Products.Value)[0].Quantity = "0"
		},
	} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			command, receipt := unassembledCandidateFixture()
			change(&command, &receipt)
			if _, err := UnassembledSplitCandidates(command, receipt); err == nil {
				t.Fatal("unbound response became readback authority")
			}
		})
	}
}
