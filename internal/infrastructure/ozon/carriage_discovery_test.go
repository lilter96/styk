package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const carriageMethodFixture = `{"delivery_method_id":20,"warehouse_id":30,"departure_date":"2026-11-07","delivery_method_status":"active","is_rfbs":false,"first_mile_changing":false,"first_mile_type":"dropoff","integration_type":"ozon","mandatory_packaged_count":1,"optional_packaged_count":1,"carriages":[],"errors":[]}`

func TestCarriageNonemptyDiscoveryRetainsScopeCountsAndProviderBlockers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body       string
		invalid, blocked bool
	}{
		{"complete method", carriageMethodFixture, false, false},
		{"provider critical", strings.Replace(carriageMethodFixture, `"errors":[]`, `"errors":[{"code":"outdated","status":"critical"}]`, 1), false, true},
		{"warning is not critical", strings.Replace(carriageMethodFixture, `"errors":[]`, `"errors":[{"code":"late","status":"warning"}]`, 1), false, false},
		{"rfbs", strings.Replace(carriageMethodFixture, `"is_rfbs":false`, `"is_rfbs":true`, 1), false, true},
		{"carrier owned", strings.Replace(carriageMethodFixture, `"integration_type":"ozon"`, `"integration_type":"other"`, 1), false, true},
		{"missing count", strings.Replace(carriageMethodFixture, `"optional_packaged_count":1,`, "", 1), true, false},
		{"null carriages", strings.Replace(carriageMethodFixture, `"carriages":[]`, `"carriages":null`, 1), true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &OrdersClient{now: func() time.Time { return time.Now().UTC() }}
			s, err := c.decodeCarriageDiscovery([]byte(`{"methods":[`+tc.body+`],"cursor":"","has_next":false}`), orderTestScope(), orders.CarriageInput{DeliveryMethodID: "20"})
			if (err != nil) != tc.invalid {
				t.Fatal(err)
			}
			if tc.invalid {
				return
			}
			if !s.Complete || s.ReadyCount != 2 || s.WarehouseID != "30" || (len(s.Blockers) > 0) != tc.blocked {
				t.Fatal(s)
			}
		})
	}
}

func TestCarriageDocumentReadbackRetainsExactAddedAndRejectsAmbiguity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"observed shape", `{"result":{"status":"ready","act_type":"ozon_digital","added_to_act":["p1"],"removed_from_act":[],"is_partial":false,"has_postings_for_next_carriage":false,"partial_num":0}}`, true},
		{"missing membership", `{"result":{"status":"ready","act_type":"ozon_digital","is_partial":false}}`, false},
		{"duplicate identity", `{"result":{"status":"ready","act_type":"ozon_digital","added_to_act":["p1","p1"],"removed_from_act":[],"is_partial":false}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeCarriageDocuments([]byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestCarriageFBSProfileDoesNotRequireRFBSMethodDirectory(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case orderWarehousePath:
			writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":30,"warehouse_type":"fbs","status":"created","is_rfbs":false,"is_express":false}],"has_next":false}`)
		case orderMethodsPath:
			writeTestResponse(t, w, `{"delivery_methods":[],"has_next":false}`)
		default:
			writeTestResponse(t, w, `{"result":{"posting_number":"p1","order_id":5001,"order_number":"synthetic-order","status":"awaiting_deliver","substatus":"posting_created","scheme":"FBS","created_at":"2026-10-29T08:00:00Z","updated_at":"2026-10-29T08:01:00Z","delivery_method":{"id":20,"warehouse_id":30},"products":[{"sku":2001,"offer_id":"offer","quantity":2,"price":"12.34","currency_code":"RUB"}]}}`)
		}
	}))
	defer server.Close()
	c := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true})
	p, err := c.ReadOrderPosting(context.Background(), orderTestScope(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	p, err = c.carriagePostingProfile(context.Background(), p, "20", "30")
	if err != nil {
		t.Fatal(err)
	}
	if issues := carriagePostingBlockers(p, "20", "30", c.now()); len(issues) > 0 {
		t.Fatalf("blockers=%+v drift=%+v", issues, p.Drift)
	}
}

func TestCarriageReadyMembershipReadsEveryPageAndChecksProviderCount(t *testing.T) {
	t.Parallel()
	for _, expected := range []int{2, 3} {
		t.Run(fmt.Sprint(expected), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case orderUnfulfilledPath:
					var body struct {
						Cursor string `json:"cursor"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if body.Cursor == "" {
						writeTestResponse(t, w, `{"postings":[{"posting_number":"p1"}],"cursor":"next","has_next":true}`)
						return
					}
					writeTestResponse(t, w, `{"postings":[{"posting_number":"p2"}],"cursor":"","has_next":false}`)
				case orderWarehousePath:
					writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":30,"warehouse_type":"fbs","status":"created","is_rfbs":false,"is_express":false}],"has_next":false}`)
				case orderMethodsPath:
					writeTestResponse(t, w, `{"delivery_methods":[],"has_next":false}`)
				case orderGetPath:
					var body struct {
						Number string `json:"posting_number"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					writeTestResponse(t, w, fmt.Sprintf(`{"result":{"posting_number":%q,"order_id":1,"status":"awaiting_deliver","delivery_method":{"id":20,"warehouse_id":30},"products":[{"sku":100,"offer_id":"offer","quantity":1}]}}`, body.Number))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			c := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true})
			snapshot := orders.CarriageSnapshot{Scope: orderTestScope(), DeliveryMethodID: "20", WarehouseID: "30"}
			err := c.collectCarriageReady(context.Background(), &snapshot, expected)
			if expected == 3 {
				if !errors.Is(err, orders.ErrStale) {
					t.Fatal("count mismatch must block", err)
				}
				return
			}
			if err != nil || len(snapshot.Members) != 2 || !snapshot.Members[0].Ready || !snapshot.Members[1].Ready {
				t.Fatal(snapshot.Members, err)
			}
		})
	}
}
