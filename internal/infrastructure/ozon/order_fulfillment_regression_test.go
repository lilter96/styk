package ozon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestSplitAcknowledgementPreservesExactChildIdentities(t *testing.T) {
	t.Parallel()
	c := &OrdersClient{}
	command := orders.Command{ID: "intent", Action: orders.ActionPostingSplit, PostingNumber: "parent", PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{}, {}}}}}
	receipt, err := c.shipReceipt(command, []byte(`{"result":["child-9007199254740993","child-2"]}`), 200)
	if err != nil || receipt.State != orders.StateAcknowledged || len(receipt.PostingNumbers) != 2 || receipt.PostingNumbers[0] != "child-9007199254740993" {
		t.Fatalf("split lineage lost: %+v %v", receipt, err)
	}
	for _, raw := range []string{`{"result":["child","child"]}`, `{"result":["child"]}`, `{"result":[]}`} {
		receipt, err = c.shipReceipt(command, []byte(raw), 200)
		if err == nil || receipt.State != orders.StateUnknown {
			t.Fatal("invalid split result accepted")
		}
	}
}

func TestFulfillmentProtectedInputIsTransientAndExact(t *testing.T) {
	t.Parallel()
	input := orders.ActionParameters{TrackingNumber: "actual-tracking"}
	command := orders.Command{Action: orders.ActionPostingTracking, PostingNumber: "posting", PostingAction: &orders.PostingAction{Parameters: input.Public(), ProtectedInput: &orders.ProtectedActionInput{Reference: "sealed", Revision: 1}}}
	if _, err := fulfillmentPayload(command, ""); err == nil {
		t.Fatal("missing secret accepted")
	}
	command.ActionInput = &input
	body, err := fulfillmentPayload(command, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil || !strings.Contains(string(encoded), "actual-tracking") || strings.Contains(string(encoded), "delivery_service") {
		t.Fatal("actual tracking not transmitted faithfully")
	}
	stored, err := json.Marshal(command)
	if err != nil || strings.Contains(string(stored), "actual-tracking") {
		t.Fatal("private tracking leaked into durable command")
	}
	wrong := orders.ActionParameters{ReasonMessage: "different action"}
	command.ActionInput = &wrong
	if _, err = fulfillmentPayload(command, ""); err == nil {
		t.Fatal("substituted secret accepted")
	}
}

func TestFulfillmentBindingDoesNotAuthorizeInvalidIntent(t *testing.T) {
	t.Parallel()
	c := &OrdersClient{}
	calls := 0
	err := c.BindOrderIntent(orders.Command{}, func(context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if err == nil || calls != 0 {
		t.Fatal("invalid binding invoked the durable dispatch guard")
	}
}

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

func TestFulfillmentGatewayTimeoutCannotBecomeSafeRejection(t *testing.T) {
	t.Parallel()
	for _, status := range []int{408, 425, 499, 502, 504} {
		response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"message":"uncertain"}`))}
		receipt, err := (&OrdersClient{}).fulfillmentResponse(orders.Command{ID: "intent"}, "", response)
		if err == nil || receipt.State != orders.StateUnknown {
			t.Fatalf("HTTP %d released uncertain target", status)
		}
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
