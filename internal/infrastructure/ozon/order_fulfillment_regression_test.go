package ozon

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestFulfillmentNumericOverflowRejected(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"18446744073709551617", "9223372036854775808", "99999999999999999999999999", "01", "+1"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := parseSKU(value); err == nil {
				t.Fatal("invalid SKU accepted")
			}
			if _, err := parseQuantity(orders.Decimal(value)); err == nil {
				t.Fatal("invalid quantity accepted")
			}
		})
	}
}

func TestFulfillmentServerErrorRemainsUnknown(t *testing.T) {
	t.Parallel()
	c := &OrdersClient{}
	response := &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"message":"unavailable"}`))}
	receipt, err := c.fulfillmentResponse(orders.Command{ID: "intent"}, "", response)
	if err == nil || receipt.State != orders.StateUnknown {
		t.Fatalf("ambiguous server failure: %+v %v", receipt, err)
	}
}

func TestFulfillmentAcknowledgementIsNotReconciliation(t *testing.T) {
	t.Parallel()
	c := &OrdersClient{}
	command := orders.Command{ID: "intent", PostingNumber: "posting"}
	receipt, err := c.cancelReceipt(command, []byte(`{"result":true}`), 200)
	if err != nil || receipt.State != orders.StateAcknowledged {
		t.Fatalf("false cancellation reconciliation: %+v %v", receipt, err)
	}
	receipt, err = c.trackingReceipt(command, []byte(`{"result":[{"posting_number":"posting","result":true,"error":""}]}`), 200)
	if err != nil || receipt.State != orders.StateAcknowledged {
		t.Fatalf("false tracking reconciliation: %+v %v", receipt, err)
	}
}
