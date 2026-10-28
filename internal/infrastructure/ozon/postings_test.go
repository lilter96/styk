package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostingV4Pages(t *testing.T) {
	var list int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/posting/fbs/list":
			var request struct {
				Cursor string `json:"cursor"`
				Limit  int    `json:"limit"`
				Filter struct {
					Since string `json:"since"`
					To    string `json:"to"`
				} `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			list++
			if request.Limit != 100 || request.Filter.Since != "2026-01-01T00:00:00Z" || request.Filter.To != "2026-01-01T01:00:00Z" || (list == 1 && request.Cursor != "") || (list == 2 && request.Cursor != "a") {
				t.Errorf("incorrect v4 page request: %+v", request)
			}
			if list == 1 {
				fmt.Fprint(w, `{"postings":[{"posting_number":"p1"}],"has_next":true,"cursor":"a"}`)
			} else {
				fmt.Fprint(w, `{"postings":[{"posting_number":"p2"}],"has_next":false}`)
			}
		case "/v3/posting/fbs/get":
			var request struct {
				PostingNumber string `json:"posting_number"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			fmt.Fprintf(w, `{"result":{"posting_number":%q,"order_id":5,"order_number":"order","status":"unknown_new","products":[{"offer_id":"a","sku":10,"quantity":2}],"customer":{"name":"PRIVATE_SENTINEL"}}}`, request.PostingNumber)
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	c := newTestClient(t, s)
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v, e := c.ReadPostings(context.Background(), "t", since, since.Add(time.Hour))
	if e != nil || len(v.Postings) != 2 || v.Postings[0].OrderID != "5" || v.Postings[1].OrderID != "5" {
		t.Fatalf("%+v %v", v, e)
	}
	encoded, err := json.Marshal(v)
	if err != nil || strings.Contains(string(encoded), "PRIVATE_SENTINEL") {
		t.Fatal("private response field retained")
	}

}
func TestPostingRejectLegacyWrapper(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":{"postings":[],"has_next":false}}`)
	}))
	defer s.Close()
	now := time.Now()
	if _, err := newTestClient(t, s).ReadPostings(context.Background(), "t", now, now.Add(time.Hour)); err == nil {
		t.Fatal("legacy wrapper accepted")
	}
}

func TestPostingEmptyAndWindowValidation(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"postings":[],"has_next":false}`)
	}))
	defer s.Close()
	c := newTestClient(t, s)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b, err := c.ReadPostings(context.Background(), "tenant", start, start.Add(time.Hour))
	if err != nil || len(b.Postings) != 0 || calls != 1 || b.FinishedAt.IsZero() {
		t.Fatalf("empty response: %+v %v", b, err)
	}
	for _, tc := range []struct {
		tenant   string
		from, to time.Time
	}{{" ", start, start.Add(time.Hour)}, {"tenant", start, start}, {"tenant", start, start.AddDate(1, 0, 1)}} {
		if _, err = c.ReadPostings(context.Background(), tc.tenant, tc.from, tc.to); err == nil {
			t.Fatal("invalid window accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid window made network requests")
	}
}

func TestPostingDetailCancellationAndUnknownFields(t *testing.T) {
	cases := []struct {
		name, extra string
		wantCancel  bool
		wantReason  string
		wantAfter   bool
	}{
		{"unknown reason", `"cancellation":{"cancel_reason":"unrecognized","cancel_reason_id":"777","cancellation_type":"vendor_new","cancellation_initiator":"other","cancelled_after_ship":true},`, true, "unrecognized", true},
		{"missing optional cancellation", ``, false, "", false},
		{"partial cancellation details", `"cancellation":{"cancel_reason_id":0},`, true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Client-Id") != "test-client" || r.Header.Get("Api-Key") != "test-key" {
					t.Error("invalid request auth")
				}
				switch r.URL.Path {
				case "/v4/posting/fbs/list":
					fmt.Fprint(w, `{"postings":[{"posting_number":"p"}],"has_next":false}`)
				case "/v3/posting/fbs/get":
					fmt.Fprintf(w, `{"result":{"posting_number":"p","order_id":1,"order_number":"order","status":"new_unknown","in_process_at":"2026-01-02T00:00:00Z","parent_posting_number":"parent","fact_delivery_date":"2026-01-03T00:00:00Z","shipment_date_without_delay":"2026-01-04T00:00:00Z",%s"products":[{"offer_id":"a","sku":99,"quantity":2}]}}`, tc.extra)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			}))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			v, err := newTestClient(t, s).ReadPostings(context.Background(), "tenant", start, start.Add(time.Hour))
			if err != nil || len(v.Postings) != 1 {
				t.Fatalf("read %v", err)
			}
			p := v.Postings[0]
			if p.Status != "new_unknown" || p.InProcessAt != "2026-01-02T00:00:00Z" || p.SourceCreatedAt != nil || p.SourceUpdatedAt != nil || p.ObservedAt.IsZero() || p.ParentPostingNumber != "parent" || p.FactDeliveryDate != "2026-01-03T00:00:00Z" || p.ShipmentDateWithoutDelay != "2026-01-04T00:00:00Z" {
				t.Fatalf("source timing or status %+v", p)
			}
			if p.CancellationPresenceKnown != tc.wantCancel {
				t.Fatalf("cancellation presence known: %+v", p)
			}
			if (p.Cancellation != nil) != tc.wantCancel {
				t.Fatalf("cancellation %+v", p.Cancellation)
			}
			if tc.wantReason != "" && (p.Cancellation.Reason == nil || *p.Cancellation.Reason != tc.wantReason || p.Cancellation.ReasonID == nil || *p.Cancellation.ReasonID != 777 || p.Cancellation.CancelledAfterShip == nil || *p.Cancellation.CancelledAfterShip != tc.wantAfter) {
				t.Fatalf("lost cancellation %+v", p.Cancellation)
			}
			if tc.name == "partial cancellation details" && (p.Cancellation.Reason != nil || p.Cancellation.CancelledAfterShip != nil) {
				t.Fatalf("missing fields became values %+v", p.Cancellation)
			}
		})
	}
}

func TestPostingRejectIncompleteTraversalAndDuplicates(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"missing list", `{}`, ``},
		{"missing has_next", `{"postings":[]}`, ``},
		{"missing cursor", `{"postings":[{"posting_number":"p"}],"has_next":true}`, ``},
		{"duplicate across pages", `{"postings":[{"posting_number":"p"}],"has_next":true,"cursor":"a"}`, `{"postings":[{"posting_number":"p"}],"has_next":false}`},
		{"repeated cursor", `{"postings":[{"posting_number":"p"}],"has_next":true,"cursor":"a"}`, `{"postings":[{"posting_number":"q"}],"has_next":true,"cursor":"a"}`},
		{"empty continuation", `{"postings":[],"has_next":true,"cursor":"a"}`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v4/posting/fbs/list" {
					page++
					if page == 1 {
						fmt.Fprint(w, tc.first)
					} else {
						fmt.Fprint(w, tc.second)
					}
					return
				}
				if r.URL.Path == "/v3/posting/fbs/get" {
					var req struct {
						PostingNumber string `json:"posting_number"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					fmt.Fprintf(w, `{"result":{"posting_number":%q,"order_id":1,"order_number":"order","status":"active","products":[]}}`, req.PostingNumber)
					return
				}
				t.Error("write or unknown endpoint")
			}))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			v, err := newTestClient(t, s).ReadPostings(context.Background(), "tenant", start, start.Add(time.Hour))
			if err == nil || len(v.Postings) != 0 {
				t.Fatalf("incomplete returned data %+v %v", v, err)
			}
		})
	}
}

func TestPostingHTTPFailuresAndCancellation(t *testing.T) {
	for _, tc := range []struct{ status, want int }{{401, 1}, {403, 1}, {429, 3}, {503, 3}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tc.status)
			}))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			_, err := newTestClient(t, s).ReadPostings(context.Background(), "tenant", start, start.Add(time.Hour))
			var api *APIError
			if !errors.As(err, &api) || api.Status != tc.status || int(calls.Load()) != tc.want {
				t.Fatalf("%v calls %d", err, calls.Load())
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(50 * time.Millisecond) }))
		defer s.Close()
		c := newTestClient(t, s)
		c.http.Timeout = 5 * time.Millisecond
		c.attempts = 1
		now := time.Now()
		if _, err := c.ReadPostings(context.Background(), "tenant", now, now.Add(time.Hour)); err == nil {
			t.Fatal("timeout accepted")
		}
	})
	t.Run("retry after cancellation", func(t *testing.T) {
		var calls atomic.Int32
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
		}))
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		now := time.Now()
		_, err := newTestClient(t, s).ReadPostings(ctx, "tenant", now, now.Add(time.Hour))
		if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
			t.Fatalf("%v calls %d", err, calls.Load())
		}
	})
}

func TestPostingReadOnlyAllowlist(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	c := newTestClient(t, s)
	for _, path := range []string{"/v3/posting/fbs/list", "/v2/posting/fbs/cancel", "/v4/posting/fbs/ship", "/v2/products/stocks"} {
		var out any
		if err := c.post(context.Background(), path, struct{}{}, &out); err == nil {
			t.Fatalf("unsafe or retired endpoint allowed: %s", path)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("disallowed operation reached server")
	}
}
