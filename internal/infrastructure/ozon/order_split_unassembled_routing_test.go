package ozon

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestUnassembledSplitUsesDistinctFulfillmentRouting(t *testing.T) {
	t.Parallel()
	command := unassembledTestCommand()
	path := fulfillmentPath(command.Action)
	if path != "/v1/posting/fbs/split" || fulfillmentPath(orders.ActionPostingSplit) != "/v4/posting/fbs/ship" {
		t.Fatal("new unassembled split rewrote historical assembly routing")
	}
	payload, err := fulfillmentPayload(command, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["postings"]; !exists {
		t.Fatal("missing split postings")
	}
	if _, exists := payload["packages"]; exists {
		t.Fatal("assembly payload used for unassembled split")
	}
	raw := []byte("{" + unassembledChildren + "}")
	receipt, err := (&OrdersClient{}).parseFulfillmentReceipt(command, path, raw, 200)
	if err != nil || receipt.State != orders.StateAcknowledged || receipt.UnassembledSplit == nil || len(receipt.PostingNumbers) != 0 {
		t.Fatal("unassembled response routed to assembly or invented confirmed allocation", err)
	}
}

func TestUnassembledSplitRejectsShipResponseAndPreservesPostDispatchUncertainty(t *testing.T) {
	t.Parallel()
	command := unassembledTestCommand()
	path := fulfillmentPath(command.Action)
	receipt, err := (&OrdersClient{}).parseFulfillmentReceipt(command, path, []byte(`{"result":["TEST-child-a","TEST-child-b"]}`), 200)
	if err == nil || receipt.State != orders.StateUnknown {
		t.Fatal("assembly response acknowledged as unassembled split")
	}
	for _, status := range []int{400, 403, 409, 429, 500} {
		response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{" + unassembledChildren + "}"))}
		receipt, err = (&OrdersClient{}).fulfillmentResponse(command, path, response)
		if err == nil || receipt.State != orders.StateUnknown || receipt.HTTPStatus != status {
			t.Fatal("dispatched non2xx lost uncertainty", status)
		}
	}
}
