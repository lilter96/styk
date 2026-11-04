package ozon

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestUnassembledSplitRejectsOversizeIntentBeforeProvider(t *testing.T) {
	t.Parallel()
	command := unassembledTestCommand()
	command.CommercialOrderID = strings.Repeat("x", maxUnassembledCommandBytes)
	if _, err := unassembledSplitPayload(command); err == nil {
		t.Fatal("oversize command had no durable ACK headroom")
	}
	command = unassembledTestCommand()
	command.PostingAction.Parameters.Packages = make([]orders.ActionPackage, 5)
	for i := range command.PostingAction.Parameters.Packages {
		lines := make([]orders.ActionQuantity, 1000)
		for j := range lines {
			lines[j] = orders.ActionQuantity{Identity: orders.LineIdentity{SKU: strconv.Itoa(j + 1)}, Quantity: "1"}
		}
		command.PostingAction.Parameters.Packages[i].Lines = lines
	}
	if _, err := unassembledSplitPayload(command); err == nil {
		t.Fatal("unbounded combined product entries accepted")
	}
}

func oversizedUnassembledResponse(t *testing.T, malformed bool) []byte {
	t.Helper()
	rows := make([]any, 10)
	for i := range rows {
		products := make([]any, 1000)
		for j := range products {
			products[j] = map[string]any{"product_id": int64(9007199254740993) + int64(j), "quantity": int64(9223372036854775807)}
		}
		if malformed && i == len(rows)-1 {
			products[len(products)-1] = map[string]any{"product_id": 0, "quantity": 1}
		}
		rows[i] = map[string]any{"posting_number": "bounded-child-" + strconv.Itoa(i), "products": products}
	}
	raw, err := json.Marshal(map[string]any{"postings": rows})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestUnassembledSplitOversizeAckPersistsBoundedUnknownWithoutFalseProof(t *testing.T) {
	t.Parallel()
	for _, malformed := range []bool{false, true} {
		t.Run(strconv.FormatBool(malformed), func(t *testing.T) {
			t.Parallel()
			receipt, err := (&OrdersClient{}).unassembledSplitReceipt(unassembledTestCommand(), oversizedUnassembledResponse(t, malformed), 200)
			if err == nil || receipt.State != orders.StateUnknown || receipt.HTTPStatus != 200 || receipt.UnassembledSplit != nil || receipt.Code != "SPLIT_UNASSEMBLED_ACK_TOO_LARGE" {
				t.Fatal("oversize/partial ACK was retained as durable allocation authority")
			}
			raw, err := json.Marshal(receipt)
			if err != nil || len(raw) > maxUnassembledReceiptBytes {
				t.Fatal("UNKNOWN receipt itself exceeded durable limit", err)
			}
		})
	}
}
