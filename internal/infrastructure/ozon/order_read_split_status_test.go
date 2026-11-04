package ozon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

// The reviewed official v3 detail schema describes this as cancellation due to
// splitting. Recognizing it does not alias it to an ordinary buyer cancellation
// or authorize releasing a native reserve without a separate split proof.
func TestOrderReaderPreservesDocumentedSplitRetirementStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestResponse(t, w, `{"result":{"posting_number":"posting-1","order_id":"5","status":"cancelled_from_split_pending","products":[{"offer_id":"offer-1","quantity":1}]}}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "posting-1")
	if err != nil {
		t.Fatal(err)
	}
	if orderKnown(posting.Status) != "cancelled_from_split_pending" || hasOrderDrift(posting, "unknown_enum") {
		t.Fatal("documented split cancellation was discarded or relabeled")
	}
	write := orders.CustomerOrderWrite{Applicable: orders.Present(true)}
	if validation := orders.ApplyOpenQuantityReserve(posting, &write, orders.PositionBaseline{}); validation.Code != "reservation_policy_status_not_pending" {
		t.Fatal("recognition incorrectly authorized ordinary reserve release")
	}
}
