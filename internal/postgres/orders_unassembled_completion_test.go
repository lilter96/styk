package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func unassembledReceiptFixture() (orders.Work, orders.Completion) {
	products := orders.Present([]orders.SplitResponseProduct{{SKU: "1", Quantity: "1"}})
	receipt := orders.Receipt{CommandID: "command", State: orders.StateAcknowledged, HTTPStatus: 200, ObservedAt: time.Unix(100, 0).UTC(), UnassembledSplit: &orders.UnassembledSplitAck{Postings: orders.Present([]orders.SplitResponsePosting{{PostingNumber: orders.Present("child"), Products: products}})}}
	work := orders.Work{State: orders.StatePolling, Command: orders.Command{Action: orders.ActionPostingSplitUnassembled}, Receipt: receipt}
	return work, orders.Completion{Receipt: receipt}
}

func TestUnassembledCompletionPreservesCanonicalAck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		alter func(*orders.Completion)
	}{
		{"changed HTTP", func(c *orders.Completion) { c.Receipt.HTTPStatus = 201 }},
		{"changed attempt clock", func(c *orders.Completion) { c.Receipt.ObservedAt = c.Receipt.ObservedAt.Add(time.Second) }},
		{"missing final receipt", func(c *orders.Completion) { c.State = orders.StateReconciled; c.Receipt = orders.Receipt{} }},
		{"dropped ACK", func(c *orders.Completion) { c.Receipt.UnassembledSplit = nil }},
		{"raw parent", func(c *orders.Completion) { c.ObservedPosting = &orders.Posting{} }},
		{"raw children", func(c *orders.Completion) { c.Receipt.ObservedPostings = []orders.Posting{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			work, completion := unassembledReceiptFixture()
			tc.alter(&completion)
			if err := validateUnassembledSplitReceiptUpdate(work, completion); !errors.Is(err, orders.ErrStale) && !errors.Is(err, orders.ErrInvalid) {
				t.Fatal("mutated canonical evidence accepted", err)
			}
		})
	}
	work, completion := unassembledReceiptFixture()
	completion.Receipt.State = orders.StateReconciled
	if err := validateUnassembledSplitReceiptUpdate(work, completion); err != nil {
		t.Fatal("status-only advancement rejected", err)
	}
}
