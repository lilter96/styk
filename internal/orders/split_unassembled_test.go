package orders

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUnassembledSplitHasDistinctActionAndReceiptFacts(t *testing.T) {
	t.Parallel()
	if !ActionPostingSplit.IsSplit() || !ActionPostingSplitUnassembled.IsSplit() || ActionPostingPack.IsSplit() || !ActionPostingSplitUnassembled.Valid() {
		t.Fatal("assembly and allocation actions were conflated")
	}
	if ActionPostingSplitUnassembled == ActionPostingSplit || ExpectedPostingStatus(ActionPostingSplitUnassembled) != "" {
		t.Fatal("a new action replaced history or fabricated a child status")
	}
	want := Receipt{CommandID: "intent", UnassembledSplit: &UnassembledSplitAck{
		Parent:   Field[SplitResponsePosting]{Presence: PresenceNull},
		Postings: Present([]SplitResponsePosting{{PostingNumber: Present("child"), Products: Present([]SplitResponseProduct{{SKU: "9007199254740993", Quantity: "2"}})}}),
	}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var actual Receipt
	if err = json.Unmarshal(raw, &actual); err != nil || !reflect.DeepEqual(want, actual) {
		t.Fatal("optional parent, exact SKU or quantity changed during durable round trip", err)
	}
	if actual.UnassembledSplit.Parent.IsKnown() || actual.UnassembledSplit.Parent.Presence != PresenceNull {
		t.Fatal("unknown original-parent allocation was represented as zero or a confirmed remainder")
	}
}
