package orders

import (
	"testing"
	"time"
)

func allocationResolutionFixture() (Posting, Work, []SplitReadback) {
	before := testPosting()
	before.Lines = before.Lines[:1]
	before.Lines[0].Quantity = "2"
	before.Lines[0].Identity.SKU = "9007199254740993"
	before.Status = Present("awaiting_packaging")
	command, receipt := unassembledCandidateFixture()
	command.Scope, command.PostingNumber, command.CommercialOrderID = before.Scope, before.PostingNumber, before.CommercialOrderID
	command.ExpectedSourceRevision = before.Evidence.Revision
	command.PostingAction = &PostingAction{Parameters: ActionParameters{Packages: []ActionPackage{
		{Lines: []ActionQuantity{{Identity: before.Lines[0].Identity, Quantity: "1"}}},
		{Lines: []ActionQuantity{{Identity: before.Lines[0].Identity, Quantity: "1"}}},
	}}}
	child := before
	child.PostingNumber = "child"
	child.Lines = append([]PostingLine(nil), before.Lines...)
	child.Lines[0].Quantity = "1"
	parent := child
	parent.PostingNumber = before.PostingNumber
	(*(*receipt.UnassembledSplit.Postings.Value)[0].Products.Value)[0].SKU = before.Lines[0].Identity.SKU
	work := Work{Scope: before.Scope, Command: command, Receipt: receipt}
	checked := time.Unix(101, 0).UTC()
	return before, work, []SplitReadback{{PostingNumber: child.PostingNumber, Posting: &child, Code: "OBSERVED", CheckedAt: checked}, {PostingNumber: parent.PostingNumber, Posting: &parent, Code: "OBSERVED", CheckedAt: checked}}
}

func TestUnassembledAllocationRequiresActualOriginalRead(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"NOT_READ", "SOURCE_CHANGED", "SOURCE_UNAVAILABLE", "OBSERVED"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			before, work, reads := allocationResolutionFixture()
			switch code {
			case "NOT_READ":
				reads = reads[:1]
			case "SOURCE_CHANGED", "SOURCE_UNAVAILABLE":
				reads[1].Code, reads[1].Posting = code, nil
			}
			result, err := ResolveUnassembledSplit(before, work, reads)
			if err != nil || result.Confirmed != (code == "OBSERVED") || result.ParentUnavailable != (code == "SOURCE_UNAVAILABLE") {
				t.Fatalf("original status=%s result=%+v err=%v", code, result, err)
			}
		})
	}
}

func TestUnassembledAllocationRejectsStaleFailureAndForeignObservation(t *testing.T) {
	t.Parallel()
	before, work, reads := allocationResolutionFixture()
	reads[1].Code, reads[1].Posting, reads[1].CheckedAt = "SOURCE_UNAVAILABLE", nil, work.Receipt.ObservedAt.Add(-time.Second)
	if _, err := ResolveUnassembledSplit(before, work, reads); err == nil {
		t.Fatal("old failure became parent-unavailable evidence")
	}
	_, _, reads = allocationResolutionFixture()
	reads[0].Posting.Scope.Tenant = "other"
	if _, err := ResolveUnassembledSplit(before, work, reads); err == nil {
		t.Fatal("foreign posting became allocation evidence")
	}
}

func TestUnassembledParentResponseDoesNotInventRetainedAllocation(t *testing.T) {
	t.Parallel()
	for _, reported := range []Decimal{"1", "2"} {
		t.Run(string(reported), func(t *testing.T) {
			t.Parallel()
			before, work, reads := allocationResolutionFixture()
			work.Receipt.UnassembledSplit.Parent = Present(SplitResponsePosting{PostingNumber: Present(before.PostingNumber), Products: Present([]SplitResponseProduct{{SKU: before.Lines[0].Identity.SKU, Quantity: reported}})})
			result, err := ResolveUnassembledSplit(before, work, reads)
			if err != nil || !result.Confirmed || len(result.PostingNumbers) != 2 {
				t.Fatal("ACK parent contents overrode independently observed allocation", err)
			}
		})
	}
}
