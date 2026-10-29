package ozon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedReadRejectsPartialTraversal(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestResponse(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"synthetic"}],"total_items":2,"last_id":"next"}}`)
	}))
	defer s.Close()
	for _, tc := range []struct {
		name     string
		limits   Limits
		resource string
	}{
		{"requests", Limits{Requests: 1, Attempts: 5, Pages: 5, Entities: 5, Products: 5, Warehouses: 5, Observations: 5, Postings: 5, ResponseBytes: 1024, Duration: time.Second}, "requests"},
		{"pages", Limits{Requests: 5, Attempts: 5, Pages: 1, Entities: 5, Products: 5, Warehouses: 5, Observations: 5, Postings: 5, ResponseBytes: 1024, Duration: time.Second}, "pages"},
		{"response", Limits{Requests: 5, Attempts: 5, Pages: 5, Entities: 5, Products: 5, Warehouses: 5, Observations: 5, Postings: 5, ResponseBytes: 5, Duration: time.Second}, "response_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, counts, err := newTestClient(t, s).ReadBounded(context.Background(), tc.limits)
			var limit *LimitError
			if !errors.As(err, &limit) || limit.Resource != tc.resource || len(snap.Products) != 0 || counts.Attempts != 1 {
				t.Fatalf("expected bounded failure without partial snapshot: %v %+v", err, counts)
			}
		})
	}
}

type countingBody struct {
	*bytes.Reader
	bytesRead int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.bytesRead += n
	return n, e
}
func (b *countingBody) Close() error { return nil }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBoundedResponseStopsReadingAtLimit(t *testing.T) {
	body := &countingBody{Reader: bytes.NewReader([]byte(strings.Repeat("x", 4096)))}
	c, e := New(Options{BaseURL: "https://example.invalid", ClientID: "synthetic", APIKey: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Header: http.Header{}}, nil
	})}})
	if e != nil {
		t.Fatal(e)
	}
	l := Limits{Requests: 1, Attempts: 1, Pages: 1, Entities: 1, Products: 1, Warehouses: 1, Observations: 1, Postings: 1, ResponseBytes: 16, Duration: time.Second}
	_, _, err := c.ReadBounded(context.Background(), l)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "response_bytes" || body.bytesRead != 17 {
		t.Fatalf("response read exceeded limit: %d %v", body.bytesRead, err)
	}
}

func TestBoundedCancellationSendsNoRequest(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, "{}") }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := Limits{Requests: 1, Attempts: 1, Pages: 1, Entities: 1, Products: 1, Warehouses: 1, Observations: 1, Postings: 1, ResponseBytes: 16, Duration: time.Second}
	_, counts, err := newTestClient(t, s).ReadBounded(ctx, l)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 || counts.Attempts != 0 {
		t.Fatal("cancelled import sent a request", err)
	}
}

func TestBoundedPostingsDoesNotSkipRequiredDetail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestResponse(t, w, `{"postings":[{"posting_number":"synthetic"}],"has_next":false,"cursor":""}`)
	}))
	defer s.Close()
	l := Limits{Requests: 1, Attempts: 1, Pages: 1, Entities: 1, Products: 1, Warehouses: 1, Observations: 1, Postings: 1, ResponseBytes: 1024, Duration: time.Second}
	now := time.Now()
	snap, counts, err := newTestClient(t, s).ReadPostingsBounded(context.Background(), "synthetic", now.Add(-time.Hour), now, l)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "requests" || counts.Attempts != 1 || len(snap.Postings) != 0 {
		t.Fatalf("missing detail was accepted: %v %+v", err, counts)
	}
}

func TestBoundedRetriesCountNetworkAttempts(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer s.Close()
	l := Limits{Requests: 5, Attempts: 1, Pages: 5, Entities: 5, Products: 5, Warehouses: 5, Observations: 5, Postings: 5, ResponseBytes: 1024, Duration: time.Second}
	_, counts, err := newTestClient(t, s).ReadBounded(context.Background(), l)
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Resource != "attempts" || counts.Requests != 1 || counts.Attempts != 1 || calls != 1 {
		t.Fatalf("retry escaped attempt budget: %v %+v calls=%d", err, counts, calls)
	}
}

func TestBoundedReadCountsRateLimitResponses(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer s.Close()
	l := Limits{Requests: 5, Attempts: 1, Pages: 5, Entities: 5, Products: 5, Warehouses: 5, Observations: 5, Postings: 5, ResponseBytes: 1024, Duration: time.Second}
	_, c, err := newTestClient(t, s).ReadBounded(context.Background(), l)
	if err == nil || c.Attempts != 1 || c.RateLimitResponses != 1 {
		t.Fatalf("rate limit was not recorded: %+v %v", c, err)
	}
}
