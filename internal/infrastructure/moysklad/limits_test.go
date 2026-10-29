package moysklad

import (
	"context"
	"errors"
	"fmt"
	"github.com/lilter96/styk/internal/domain"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedReadCountsRateLimitResponses(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer s.Close()
	l := domain.DefaultMoyskladImportLimits()
	l.Attempts = 1
	_, counts, err := client(t, s).ReadBoundedProgress(context.Background(), "acct", l, nil)
	if err == nil || counts.Attempts != 1 || counts.RateLimitResponses != 1 {
		t.Fatalf("rate limit was not recorded: %+v %v", counts, err)
	}
}

func TestCatalogBudgetsStopBeforeNextSend(t *testing.T) {
	for _, resource := range []string{"requests", "attempts", "pages", "products", "variants", "warehouses", "observations", "warehouse_observations", "response_bytes"} {
		t.Run(resource, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if resource == "attempts" {
					w.WriteHeader(500)
					return
				}
				var row string
				switch r.URL.Path {
				case "/entity/product", "/entity/variant", "/entity/store":
					row = `{"id":"a"}`
				case "/report/stock/all":
					row = `{"meta":{"href":"/entity/product/a","type":"product"},"stock":0,"reserve":0,"inTransit":0,"quantity":0}`
				case "/report/stock/bystore":
					row = `{"meta":{"href":"/entity/product/a","type":"product"},"stockByStore":[{"meta":{"href":"/entity/store/a","type":"store"},"stock":0,"reserve":0,"inTransit":0},{"meta":{"href":"/entity/store/b","type":"store"},"stock":1,"reserve":0,"inTransit":0}]}`
				default:
					t.Error("unexpected request")
				}
				// Two distinct rows over two pages on each collection.
				if r.URL.Query().Get("offset") == "1" {
					switch r.URL.Path {
					case "/entity/product", "/entity/variant", "/entity/store":
						row = `{"id":"b"}`
					case "/report/stock/all":
						row = `{"meta":{"href":"/entity/product/b","type":"product"},"stock":0,"reserve":0,"inTransit":0,"quantity":0}`
					case "/report/stock/bystore":
						row = `{"meta":{"href":"/entity/product/b","type":"product"},"stockByStore":[]}`
					}
				}
				fmt.Fprintf(w, `{"meta":{"size":2,"limit":1,"offset":%s},"rows":[%s]}`, r.URL.Query().Get("offset"), row)
			}))
			defer srv.Close()
			l := domain.DefaultMoyskladImportLimits()
			switch resource {
			case "requests":
				l.Requests = 1
			case "attempts":
				l.Attempts = 1
			case "pages":
				l.Pages = 1
			case "products":
				l.Products = 1
			case "variants":
				l.Variants = 1
			case "warehouses":
				l.Warehouses = 1
			case "observations":
				l.Observations = 1
			case "warehouse_observations":
				l.WarehouseObservations = 1
			case "response_bytes":
				l.ResponseBytes = 8
			}
			_, c, err := client(t, srv).ReadBoundedProgress(context.Background(), "acct", l, nil)
			var limit *LimitError
			if !errors.As(err, &limit) || limit.Resource != resource {
				t.Fatalf("expected %s, got %v", resource, err)
			}
			if c.Attempts != int(calls.Load()) {
				t.Fatal("network attempts not counted")
			}
			if (resource == "requests" || resource == "attempts" || resource == "pages" || resource == "response_bytes") && calls.Load() != 1 {
				t.Fatal("sent after budget exhausted")
			}
		})
	}
}
func TestBoundedCancellationAndDeadline(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); <-r.Context().Done() }))
	defer s.Close()
	for _, cancelNow := range []bool{true, false} {
		l := domain.DefaultMoyskladImportLimits()
		l.DurationNS = int64(20 * time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		if cancelNow {
			cancel()
		}
		_, _, err := client(t, s).ReadBoundedProgress(ctx, "acct", l, nil)
		cancel()
		if cancelNow && !errors.Is(err, context.Canceled) || !cancelNow && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("cancellation sent further requests")
	}
}

type countedBody struct{ n int }

func (b *countedBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.n += len(p)
	return len(p), nil
}
func (*countedBody) Close() error { return nil }

type boundedTransport struct{ body *countedBody }

func (rt boundedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: rt.body}, nil
}
func TestBodyLimitAppliesWhileReading(t *testing.T) {
	body := &countedBody{}
	c, err := New(Options{BaseURL: "https://synthetic.invalid/", Token: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1, HTTPClient: &http.Client{Transport: boundedTransport{body}}})
	if err != nil {
		t.Fatal(err)
	}
	l := domain.DefaultMoyskladImportLimits()
	l.ResponseBytes = 8
	_, _, err = c.ReadBoundedProgress(context.Background(), "synthetic", l, nil)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "response_bytes" || body.n != 9 {
		t.Fatalf("unbounded read: bytes=%d err=%v", body.n, err)
	}
}

func TestBoundedGETsDoNotUseTransparentTransportReplay(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !r.Close {
			t.Error("bounded transport reused a connection")
		}
		fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
	}))
	defer s.Close()
	_, c, err := client(t, s).ReadBoundedProgress(context.Background(), "acct", domain.DefaultMoyskladImportLimits(), nil)
	if err != nil || c.Attempts != 5 || calls.Load() != 5 {
		t.Fatal("unexpected network attempts", c, err)
	}
}

func TestBoundedCustomerOrdersStopBeforeAnotherRequest(t *testing.T) {
	var attempts atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.URL.Path != "/entity/customerorder" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("unexpected order request %s %s", r.URL.Path, r.URL.RawQuery)
			http.Error(w, "invalid", 500)
			return
		}
		if _, err := fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"accountId":"test-account"}]}`, testOrderA); err != nil {
			t.Errorf("write synthetic response: %v", err)
		}
	}))
	defer s.Close()
	limits := domain.DefaultMoyskladImportLimits()
	limits.Requests = 1
	limits.Attempts = 1
	limits.Pages = 2
	now := time.Now().UTC().Truncate(time.Millisecond)
	got, counts, err := orderClient(t, s).ReadCustomerOrdersBounded(context.Background(), "tenant", "test-account", now.Add(-time.Hour), now, limits)
	var limit *LimitError
	if !errors.As(err, &limit) || attempts.Load() != 1 || counts.Attempts != 1 || len(got.Orders) != 0 || !got.StartedAt.IsZero() {
		t.Fatalf("partial order traversal escaped budget: attempts=%d counts=%+v batch=%+v err=%v", attempts.Load(), counts, got, err)
	}
}
