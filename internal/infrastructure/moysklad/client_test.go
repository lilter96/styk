package moysklad

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func client(t *testing.T, s *httptest.Server) *Client {
	t.Helper()
	c, e := New(Options{BaseURL: s.URL + "/", Token: "test-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: 2 * time.Second, Attempts: 3, PageSize: 1})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestReadPaginationAndValues(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("incorrect auth or method")
		}
		offset := r.URL.Query().Get("offset")
		switch r.URL.Path {
		case "/entity/product":
			if offset == "0" {
				fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":0},"rows":[{"id":"p1","accountId":"acct","name":"Item","unknown":7}]}`)
			} else {
				fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":1},"rows":[{"id":"p2"}]}`)
			}
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":"v1","product":{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/p1","type":"product"}}}]}`)
		case "/entity/store":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":"s1"}]}`)
		case "/report/stock/bystore":
			fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
		case "/report/stock/all":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/p1","type":"product"},"stock":4.25,"reserve":2,"inTransit":1,"quantity":3.25}]}`)
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	snap, err := client(t, s).Read(context.Background(), "acct")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Entities) != 4 || len(snap.Stock) != 1 || *snap.Stock[0].Stock != "4.25" || *snap.Stock[0].Quantity != "3.25" || snap.Stock[0].FreeStock != nil || snap.Entities[2].ParentID != "p1" {
		t.Fatalf("unexpected import: %+v", snap)
	}
}
func TestIncompleteAndDuplicates(t *testing.T) {
	for _, tc := range []struct{ name, body string }{{"missing rows", `{"meta":{"size":0,"limit":1,"offset":0}}`}, {"empty interrupted", `{"meta":{"size":1,"limit":1,"offset":0},"rows":[]}`}, {"duplicate", `{"meta":{"size":2,"limit":2,"offset":0},"rows":[{"id":"x"},{"id":"x"}]}`}, {"missing id", `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{}]}`}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer s.Close()
			if _, err := client(t, s).Read(context.Background(), "acct"); err == nil {
				t.Fatal("incomplete response accepted")
			}
		})
	}
}
func TestIncompleteStock(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "stock") {
			fmt.Fprint(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"meta":{"href":"/entity/product/a","type":"product"},"stock":0}]}`)
		} else {
			fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
		}
	}))
	defer s.Close()
	if _, err := client(t, s).Read(context.Background(), "acct"); err == nil {
		t.Fatal("partial stock row accepted")
	}
}
func TestStatusesAndRetry(t *testing.T) {
	for _, tc := range []struct{ status, want int }{{401, 1}, {403, 1}, {429, 3}, {500, 3}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var n atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				if tc.status == 429 {
					w.Header().Set("Retry-After", "0")
				}
				w.WriteHeader(tc.status)
			}))
			defer s.Close()
			_, err := client(t, s).Read(context.Background(), "acct")
			var api *APIError
			if !errors.As(err, &api) || api.Status != tc.status || int(n.Load()) != tc.want {
				t.Fatalf("attempts=%d err=%v", n.Load(), err)
			}
		})
	}
}
func TestRetryAfterAndCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "1"); w.WriteHeader(429) }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client(t, s).Read(ctx, "acct")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("retry cancellation: %v", err)
	}
	if d, ok := retryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat), time.Now()); !ok || d < 50*time.Second {
		t.Fatalf("date retry-after %v", d)
	}
}
func TestTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(80 * time.Millisecond) }))
	defer s.Close()
	c, e := New(Options{BaseURL: s.URL + "/", Token: "test", Timeout: 10 * time.Millisecond, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if e != nil {
		t.Fatal(e)
	}
	if _, err := c.Read(context.Background(), "acct"); err == nil {
		t.Fatal("timeout accepted")
	}
}

func TestPageInterruptAndRetrySuccess(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/entity/product" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			return
		}
		if r.URL.Query().Get("offset") == "0" {
			fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":0},"rows":[{"id":"p1"}]}`)
			return
		}
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	if _, err := client(t, s).Read(context.Background(), "acct"); err == nil || requests.Load() != 3 {
		t.Fatalf("interrupted page: %v, attempts %d", err, requests.Load())
	}
	var attempts atomic.Int32
	retry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"meta":{"size":0,"limit":1,"offset":0},"rows":[]}`)
	}))
	defer retry.Close()
	snap, err := client(t, retry).Read(context.Background(), "acct")
	if err != nil || len(snap.Entities) != 0 || len(snap.Stock) != 0 || attempts.Load() != 6 {
		t.Fatalf("retry: %+v %v, requests %d", snap, err, attempts.Load())
	}
}
