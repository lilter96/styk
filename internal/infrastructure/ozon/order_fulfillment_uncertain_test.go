package ozon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func TestFulfillmentHTTPRejectionDoesNotProveNoExternalEffect(t *testing.T) {
	t.Parallel()
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"code":9,"message":"PRIVATE_PROVIDER_ERROR"}`))}
			receipt, err := (&OrdersClient{}).fulfillmentResponse(orders.Command{ID: "intent"}, "", response)
			if !errors.Is(err, orders.ErrStale) || receipt.State != orders.StateUnknown || receipt.HTTPStatus != status || receipt.Code != "PROVIDER_UNCERTAIN" {
				t.Fatalf("HTTP status invented safe rejection: state=%s code=%s HTTP=%d err=%v", receipt.State, receipt.Code, receipt.HTTPStatus, err)
			}
		})
	}
}

func TestFulfillmentAppliedCancellationThenNonSuccessStaysUnknown(t *testing.T) {
	t.Parallel()
	for _, status := range []int{400, 401, 403, 404, 409, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var applied, authorized atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/posting/fbs/cancel" || r.Method != http.MethodPost || r.Header.Get("Client-Id") != "id" {
					t.Error("wrong exact mutation target")
					w.WriteHeader(400)
					return
				}
				applied.Add(1) // Simulate committed provider effect before an error response.
				w.WriteHeader(status)
				writeTestResponse(t, w, `{"code":9,"message":"PRIVATE_PROVIDER_ERROR"}`)
			}))
			defer server.Close()
			client := uncertainFulfillmentClient(t, server)
			command := rfbsAdapterCommand(t, rfbsAdapterPosting())
			command.Action = orders.ActionPostingCancel
			command.PostingAction.ContractHash = orderactions.ExecutionContractHash()
			command.PostingAction.Parameters.ReasonID = "1"
			if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { authorized.Add(1); return true, nil }); err != nil {
				t.Fatal(err)
			}
			receipt, err := client.WriteOrder(context.Background(), command)
			if !errors.Is(err, orders.ErrStale) || receipt.State != orders.StateUnknown || receipt.HTTPStatus != status || applied.Load() != 1 || authorized.Load() != 1 {
				t.Fatalf("applied mutation lost UNKNOWN: state=%s HTTP=%d calls=%d guard=%d err=%v", receipt.State, receipt.HTTPStatus, applied.Load(), authorized.Load(), err)
			}
			if _, err = client.WriteOrder(context.Background(), command); err == nil || applied.Load() != 1 {
				t.Fatal("consumed exact intent was sent again")
			}
		})
	}
}

func uncertainFulfillmentClient(t *testing.T, server *httptest.Server) *OrdersClient {
	t.Helper()
	client := newOrderTestClient(t, server, OrderReadOptions{})
	gate, err := providercall.New(&labelAdmissionStore{}, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.read.Coordinate(gate, providercall.Scope{Tenant: client.scope.Tenant, Provider: "ozon", Account: client.scope.OzonAccount, CredentialRevision: 1}); err != nil {
		t.Fatal(err)
	}
	return client
}
