package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func timeNowUTC() time.Time { return time.Now().UTC() }

func TestCarriageDiscoveryUsesObservedPrimaryEmptyContract(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]json.RawMessage
		if r.Method != "POST" || r.URL.Path != carriageDiscoveryPath || r.Header.Get("Client-Id") != "id" || r.Header.Get("Api-Key") == "" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || string(body["limit"]) != "1000" {
			t.Error("discovery contract/auth changed")
		}
		writeTestResponse(t, w, `{"methods":[],"cursor":"","has_next":false}`)
	}))
	defer server.Close()
	c := newOrderTestClient(t, server, OrderReadOptions{})
	traceable := false
	snapshot, err := c.ReadCarriageBatch(context.Background(), orderTestScope(), orders.CarriageInput{Action: orders.CarriageCreate, DeliveryMethodID: "9007199254740993", AllBLRTraceable: &traceable})
	if err != nil || !snapshot.Complete || len(snapshot.Members) != 0 || len(snapshot.Blockers) != 1 || snapshot.Blockers[0].Code != "CARRIAGE_NO_AVAILABLE_BATCH" || calls.Load() != 1 {
		t.Fatalf("empty source must be explicit read-only blocked preview: %+v %v", snapshot, err)
	}
}

func TestCarriageDiscoveryNeverGuessesNonemptyOrIncompleteMembership(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		wantError  bool
	}{
		{"unverified method", `{"methods":[{"unknown":"opaque"}],"cursor":"","has_next":false}`, false},
		{"unverified page", `{"methods":[],"cursor":"next","has_next":true}`, false},
		{"null methods", `{"methods":null,"cursor":"","has_next":false}`, true},
		{"missing methods", `{"cursor":"","has_next":false}`, true},
		{"missing cursor", `{"methods":[],"has_next":false}`, true},
		{"null cursor", `{"methods":[],"cursor":null,"has_next":false}`, true},
		{"missing completion", `{"methods":[],"cursor":""}`, true},
		{"unknown field", `{"methods":[],"cursor":"","has_next":false,"new_requirement":true}`, true},
		{"duplicate key", `{"methods":[],"methods":[],"cursor":"","has_next":false}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &OrdersClient{now: timeNowUTC}
			snapshot, err := c.decodeCarriageDiscovery([]byte(tc.body), orderTestScope(), orders.CarriageInput{DeliveryMethodID: "1"})
			if tc.wantError {
				if !errors.Is(err, orders.ErrSchemaDrift) {
					t.Fatal("incomplete discovery accepted", err)
				}
				return
			}
			if err != nil || snapshot.Complete || len(snapshot.Blockers) == 0 || len(snapshot.SourceShapeFingerprint) != 64 {
				t.Fatal("unverified shape granted membership", snapshot, err)
			}
		})
	}
}

func TestCarriageWriteReceiptsPreserveUncertaintyAndLargeIDs(t *testing.T) {
	t.Parallel()
	c := &OrdersClient{now: timeNowUTC}
	tests := []struct {
		name   string
		action orders.CarriageAction
		status int
		body   string
		ack    bool
	}{
		{"create exact ID", orders.CarriageCreate, 200, `{"carriage_id":9007199254740993}`, true},
		{"create missing", orders.CarriageCreate, 200, `{}`, false},
		{"create zero", orders.CarriageCreate, 200, `{"carriage_id":0}`, false},
		{"create overflow", orders.CarriageCreate, 200, `{"carriage_id":9223372036854775808}`, false},
		{"create extra outcome", orders.CarriageCreate, 200, `{"carriage_id":12,"error":"partial"}`, false},
		{"approve empty", orders.CarriageApprove, 200, ``, true},
		{"approve foreign JSON", orders.CarriageApprove, 200, `{"result":true}`, false},
		{"forbidden", orders.CarriageCreate, 403, `{}`, false},
		{"rate limit", orders.CarriageCreate, 429, `{}`, false},
		{"unavailable", orders.CarriageCreate, 503, `{}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := orders.CarriagePlan{ID: "command", CarriageInput: orders.CarriageInput{Action: tc.action, CarriageID: "9007199254740993"}}
			receipt, err := c.carriageWriteResponse(plan, &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))})
			if tc.ack {
				if err != nil || receipt.State != orders.StateAcknowledged || receipt.ExternalID != "9007199254740993" {
					t.Fatal(receipt, err)
				}
				return
			}
			if err == nil || receipt.State != orders.StateUnknown {
				t.Fatal("uncertain response became known unsent", receipt, err)
			}
		})
	}
}
