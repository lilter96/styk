package orders

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func numericSplitFixture[T any](t *testing.T, input T) T {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(`"sku-100"`), []byte(`"100"`))
	var output T
	if err = json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func acknowledgeUnassembledFixture(work *Work) {
	work.Command.Action = ActionPostingSplitUnassembled
	work.Receipt.ObservedAt = time.Unix(100, 0).UTC()
	outputs := make([]SplitResponsePosting, 0, len(work.Receipt.ObservedPostings))
	for i := range work.Receipt.ObservedPostings {
		child := &work.Receipt.ObservedPostings[i]
		child.Status = Present("awaiting_packaging")
		products := make([]SplitResponseProduct, 0, len(child.Lines))
		for _, line := range child.Lines {
			products = append(products, SplitResponseProduct{SKU: line.Identity.SKU, Quantity: line.Quantity})
		}
		outputs = append(outputs, SplitResponsePosting{PostingNumber: Present(child.PostingNumber), Products: Present(products)})
	}
	work.Receipt.UnassembledSplit = &UnassembledSplitAck{Postings: Present(outputs)}
}

func TestUnassembledTransferStillRequiresAcknowledgedAllocationAndNativeReduction(t *testing.T) {
	t.Parallel()
	facts := numericSplitFixture(t, splitTransferFixture(t))
	acknowledgeUnassembledFixture(&facts.Split)
	facts.Parent.Status, facts.Child.Status = Present("awaiting_packaging"), Present("awaiting_packaging")
	if _, err := NewSplitReserveTransfer(facts); err != nil {
		t.Fatal("valid unassembled allocation with completed owned reduction blocked", err)
	}
	facts.Split.Receipt.HTTPStatus = 201
	if _, err := NewSplitReserveTransfer(facts); err == nil {
		t.Fatal("unverified ACK status granted reserve permission")
	}
	facts.Split.Receipt.HTTPStatus = 200
	(*(*facts.Split.Receipt.UnassembledSplit.Postings.Value)[0].Products.Value)[0].Quantity = "3"
	if _, err := NewSplitReserveTransfer(facts); err == nil {
		t.Fatal("ACK/allocation mismatch granted reserve permission")
	}
}

func TestUnassembledSupersededProofPreservesAckAcrossFrozenWitness(t *testing.T) {
	t.Parallel()
	facts, _ := supersededSplitFixture(t)
	facts = numericSplitFixture(t, facts)
	acknowledgeUnassembledFixture(&facts.Split)
	facts.Children = facts.Split.Receipt.ObservedPostings
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal("valid unassembled superseded proof blocked", err)
	}
	if err = proof.Validate(); err != nil {
		t.Fatal("frozen proof lost original ACK", err)
	}
	facts.Parent.Status = Present("cancelled")
	if _, err = NewSupersededSplitReserve(facts); err == nil {
		t.Fatal("generic cancellation became split retirement")
	}
}
