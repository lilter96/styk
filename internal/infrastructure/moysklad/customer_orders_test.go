package moysklad

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testOrderA = "10000000-0000-0000-0000-000000000001"
const testOrderB = "10000000-0000-0000-0000-000000000002"

func orderClient(t *testing.T, s *httptest.Server) *Client {
	t.Helper()
	// An ephemeral test-only header value; no customer credentials are fixtures.
	c, err := New(Options{BaseURL: s.URL + "/", Token: fmt.Sprintf("mock-%d", time.Now().UnixNano()), Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 2, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCustomerOrdersPagesAndNullableFields(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "unexpected", 405)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer mock-") {
			t.Error("authentication header missing")
		}
		switch r.URL.Path {
		case "/entity/customerorder":
			if got := r.URL.Query().Get("filter"); got != "updated>=2026-10-29 12:00:00.000;updated<2026-10-30 12:00:00.000" {
				t.Errorf("wrong bounded MSK filter %q", got)
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			id := testOrderA
			if offset == 1 {
				id = testOrderB
			}
			fmt.Fprintf(w, `{"meta":{"size":2,"limit":1,"offset":%d},"rows":[{"id":%q,"accountId":"test-account","updated":"2026-10-29 13:00:00.000"}]}`, offset, id)
		case "/entity/customerorder/" + testOrderA, "/entity/customerorder/" + testOrderB:
			id := strings.TrimPrefix(r.URL.Path, "/entity/customerorder/")
			fmt.Fprintf(w, `{"id":%q,"accountId":"test-account","updated":"2026-10-29 13:00:00.000","created":"2026-10-29 12:00:00.000","deleted":"2026-10-29 14:00:00.000","applicable":false,"store":{"meta":{"type":"store","href":"/entity/store/60000000-0000-0000-0000-000000000001"}},"state":{"meta":{"type":"state","href":"/entity/customerorder/metadata/states/70000000-0000-0000-0000-000000000001"}},"agent":{"name":"sensitive-not-retained"},"shipmentAddress":"sensitive-not-retained","description":"sensitive-not-retained","externalCode":"posting-number-is-not-proof"}`, id)
		case "/entity/customerorder/" + testOrderA + "/positions":
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if offset == 0 {
				fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":0},"rows":[{"id":"30000000-0000-0000-0000-000000000001","accountId":"test-account","quantity":3.5,"reserve":0,"shipped":1,"assortment":{"meta":{"type":"variant","href":"/entity/variant/40000000-0000-0000-0000-000000000001"}},"pack":{"id":"80000000-0000-0000-0000-000000000001","quantity":2,"uom":{"meta":{"type":"uom","href":"/entity/uom/90000000-0000-0000-0000-000000000001"}}}}]}`)
			} else {
				fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":1},"rows":[{"id":"30000000-0000-0000-0000-000000000002","accountId":"test-account","quantity":1,"assortment":{"meta":{"type":"product","href":"/entity/product/50000000-0000-0000-0000-000000000001"}}}]}`)
			}
		case "/entity/customerorder/" + testOrderB + "/positions":
			fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	since := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	until := since.Add(24 * time.Hour)
	b, err := orderClient(t, server).ReadCustomerOrders(context.Background(), "tenant", "test-account", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 0 || len(b.Orders) != 2 || len(b.Orders[0].Positions) != 2 {
		t.Fatalf("unexpected traversal: %+v", b)
	}
	o := b.Orders[0]
	p := o.Positions[0]
	if o.Deleted == nil || o.Applicable == nil || *o.Applicable || o.StateID == nil || *o.StateID != "70000000-0000-0000-0000-000000000001" || o.WarehouseID == nil || *o.WarehouseID != "60000000-0000-0000-0000-000000000001" || p.AssortmentKind != "variant" || *p.Reserve != "0" || *p.Quantity != "3.5" || *p.Shipped != "1" || p.PackID == nil || *p.PackQuantity != "2" || *p.PackUnitID != "90000000-0000-0000-0000-000000000001" {
		t.Fatalf("values not preserved: %+v %+v", o, p)
	}
	if o.Positions[1].Reserve != nil || o.Positions[1].Shipped != nil || o.ObservedAt.IsZero() || p.ObservedAt.IsZero() {
		t.Fatal("missing fields became values or local timestamps were lost")
	}
}

func TestCustomerOrdersEmptyAndReadAllowlist(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
	}))
	defer s.Close()
	c := orderClient(t, s)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b, err := c.ReadCustomerOrders(context.Background(), "tenant", "account", start, start.Add(time.Hour))
	if err != nil || len(b.Orders) != 0 {
		t.Fatalf("empty collection: %+v %v", b, err)
	}
	for _, path := range []string{"entity/customerorder/delete", "entity/customerorder/../../product", "entity/customerorder/" + testOrderA + "/positions/delete", "entity/paymentin"} {
		if _, err = c.get(context.Background(), path, nil); err == nil {
			t.Fatalf("unapproved path %s accepted", path)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("allowlist rejection contacted server")
	}
	if _, err = c.ReadCustomerOrders(context.Background(), "tenant", "account", start, start.Add(32*24*time.Hour)); err == nil {
		t.Fatal("unbounded window accepted")
	}
}

func TestCustomerOrderMissingTimestampAndUnsupportedAssortment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		positions string
		wantError bool
	}{
		{"blank list update remains unknown", `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`, false},
		{"unknown assortment is rejected", `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":"30000000-0000-0000-0000-000000000001","accountId":"acct","assortment":{"meta":{"type":"sensitive-unrecognized-type","href":"/entity/sensitive-unrecognized-type/40000000-0000-0000-0000-000000000001"}}}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/entity/customerorder":
					fmt.Fprintf(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":"acct","updated":" "}]}`, testOrderA)
				case "/entity/customerorder/" + testOrderA:
					fmt.Fprintf(w, `{"id":%q,"accountId":"acct","updated":"2026-01-01 00:00:00"}`, testOrderA)
				case "/entity/customerorder/" + testOrderA + "/positions":
					fmt.Fprint(w, tc.positions)
				default:
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			batch, err := orderClient(t, s).ReadCustomerOrders(context.Background(), "t", "acct", start, start.Add(time.Hour))
			if tc.wantError {
				if err == nil || strings.Contains(err.Error(), "sensitive-unrecognized-type") {
					t.Fatalf("unexpected assortment accepted or disclosed: %v", err)
				}
				return
			}
			if err != nil || len(batch.Orders) != 1 || batch.Orders[0].Updated == nil || *batch.Orders[0].Updated != "2026-01-01 00:00:00" {
				t.Fatalf("blank list timestamp treated as source update: %+v %v", batch, err)
			}
		})
	}
}

func TestCustomerOrdersIncompleteAndDuplicatePagination(t *testing.T) {
	for _, tc := range []struct {
		name          string
		positionBody  string
		detailUpdated string
	}{
		{"incomplete positions", `{"meta":{"size":2,"limit":1,"offset":0},"rows":[]}`, "2026-01-01 00:00:00"},
		{"duplicate positions", `{"meta":{"size":2,"limit":2,"offset":0},"rows":[{"id":"30000000-0000-0000-0000-000000000001","accountId":"acct"},{"id":"30000000-0000-0000-0000-000000000001","accountId":"acct"}]}`, "2026-01-01 00:00:00"},
		{"changed detail", ``, "2026-01-02 00:00:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/entity/customerorder":
					fmt.Fprintf(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":"acct","updated":"2026-01-01 00:00:00"}]}`, testOrderA)
				case "/entity/customerorder/" + testOrderA:
					fmt.Fprintf(w, `{"id":%q,"accountId":"acct","updated":%q}`, testOrderA, tc.detailUpdated)
				default:
					fmt.Fprint(w, tc.positionBody)
				}
			}))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := orderClient(t, s).ReadCustomerOrders(context.Background(), "t", "acct", start, start.Add(time.Hour)); err == nil {
				t.Fatal("incomplete import accepted")
			}
		})
	}
}

func TestCustomerOrdersDuplicateIdentifiersAndAccountMismatch(t *testing.T) {
	for _, tc := range []struct{ name, list, detail string }{
		{"duplicate order UUID", fmt.Sprintf(`{"meta":{"size":2,"limit":2,"offset":0},"rows":[{"id":%q,"accountId":"acct"},{"id":%q,"accountId":"acct"}]}`, testOrderA, testOrderA), fmt.Sprintf(`{"id":%q,"accountId":"acct"}`, testOrderA)},
		{"wrong account", fmt.Sprintf(`{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":"other"}]}`, testOrderA), ""},
		{"detail wrong account", fmt.Sprintf(`{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":"acct"}]}`, testOrderA), fmt.Sprintf(`{"id":%q,"accountId":"other"}`, testOrderA)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/entity/customerorder":
					fmt.Fprint(w, tc.list)
				case "/entity/customerorder/" + testOrderA:
					fmt.Fprint(w, tc.detail)
				default:
					fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
				}
			}))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := orderClient(t, s).ReadCustomerOrders(context.Background(), "t", "acct", start, start.Add(time.Hour)); err == nil {
				t.Fatal("duplicate or cross-account order accepted")
			}
		})
	}
}

func TestCustomerOrderTimeoutAndAuthentication(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(status) }))
			defer s.Close()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := orderClient(t, s).ReadCustomerOrders(context.Background(), "t", "acct", start, start.Add(time.Hour)); err == nil || calls.Load() != 1 {
				t.Fatalf("authentication failure retries: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	c, err := New(Options{BaseURL: s.URL + "/", Token: fmt.Sprintf("mock-%d", time.Now().UnixNano()), Timeout: 10 * time.Millisecond, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err = c.ReadCustomerOrders(context.Background(), "t", "acct", start, start.Add(time.Hour)); err == nil {
		t.Fatal("timeout ignored")
	}
}

func TestCustomerOrderSameNameAcrossAccountsDoesNotEstablishIdentity(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, account := range []string{"account-a", "account-b"} {
		id := testOrderA
		if i == 1 {
			id = testOrderB
		}
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/entity/customerorder":
				fmt.Fprintf(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":%q,"name":"same-order-name"}]}`, id, account)
			case "/entity/customerorder/" + id:
				fmt.Fprintf(w, `{"id":%q,"accountId":%q,"name":"same-order-name"}`, id, account)
			default:
				fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
			}
		}))
		b, err := orderClient(t, s).ReadCustomerOrders(context.Background(), "tenant", account, start, start.Add(time.Hour))
		s.Close()
		if err != nil || len(b.Orders) != 1 || b.Orders[0].ID != id || b.Orders[0].Account != account || strings.Contains(fmt.Sprintf("%+v", b), "same-order-name") {
			t.Fatalf("name or account identity mishandled: %v", err)
		}
	}
}

func TestCustomerOrdersCancellationAndRetry(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
	}))
	defer s.Close()
	c := orderClient(t, s)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.ReadCustomerOrders(context.Background(), "t", "a", start, start.Add(time.Hour)); err != nil || calls.Load() != 2 {
		t.Fatalf("retry: %v calls=%d", err, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ReadCustomerOrders(ctx, "t", "a", start, start.Add(time.Hour)); err == nil {
		t.Fatal("cancel ignored")
	}
}
