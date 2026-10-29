package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, s *httptest.Server) *Client {
	t.Helper()
	c, e := New(Options{BaseURL: s.URL, ClientID: "test-client", APIKey: "test-key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: 2 * time.Second, Attempts: 3, PageSize: 2})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestRead(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Client-Id") != "test-client" || r.Header.Get("Api-Key") != "test-key" {
			t.Error("wrong authentication or method")
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case productPath:
			f := b["filter"].(map[string]any)
			if f["visibility"] == "ARCHIVED" {
				writeTestResponse(t, w, `{"result":{"items":[{"product_id":3,"offer_id":"old","sku":33,"archived":true}],"total_items":1,"last_id":"arch"}}`)
			} else if b["last_id"] == "" {
				writeTestResponse(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"one","sku":11,"archived":false}],"total_items":2,"last_id":"p1"}}`)
			} else {
				writeTestResponse(t, w, `{"result":{"items":[{"product_id":2,"offer_id":"two","sku":22,"archived":false}],"total_items":2,"last_id":"p2"}}`)
			}
		case warehousePath:
			if b["cursor"] == "" {
				writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":100,"name":"A","is_rfbs":false}],"has_next":true,"cursor":"w1"}`)
			} else {
				writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":200,"name":"B","is_rfbs":true}],"has_next":false,"cursor":""}`)
			}
		case stockPath:
			if b["cursor"] == "" {
				writeTestResponse(t, w, `{"products":[{"product_id":1,"offer_id":"one","sku":11,"warehouse_id":100,"present":3,"reserved":1,"free_stock":2}],"has_next":true,"cursor":"s1"}`)
			} else {
				writeTestResponse(t, w, `{"products":[{"product_id":1,"offer_id":"one","sku":11,"warehouse_id":200,"present":0,"reserved":0,"free_stock":0}],"has_next":false,"cursor":""}`)
			}
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	snap, err := newTestClient(t, s).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Products) != 3 || len(snap.Warehouses) != 2 || len(snap.Stocks) != 2 || snap.Products[0].ProductID == snap.Products[0].SKU || snap.Stocks[1].Present != 0 || !(*snap.Products[2].Archived) {
		t.Fatalf("unexpected result %+v", snap)
	}
}
func TestCredentials(t *testing.T) {
	_, err := New(Options{BaseURL: "https://api-seller.ozon.ru"})
	if err == nil {
		t.Fatal("missing credentials accepted")
	}
}
func TestStatus(t *testing.T) {
	for _, tc := range []struct{ status, count int }{{401, 1}, {403, 1}, {429, 3}, {503, 3}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tc.status)
				writeTestResponse(t, w, `{"code":42}`)
			}))
			defer s.Close()
			_, err := newTestClient(t, s).Read(context.Background())
			var api *APIError
			if !errors.As(err, &api) || api.Status != tc.status || api.Code != "42" || calls.Load() != int32(tc.count) {
				t.Fatalf("error %v calls %d", err, calls.Load())
			}
		})
	}
}
func TestRetryAfterCancel(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "1"); w.WriteHeader(429) }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := newTestClient(t, s).Read(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, ok := retryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)); !ok {
		t.Fatal("invalid retry date")
	}
}
func TestTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(60 * time.Millisecond) }))
	defer s.Close()
	c := newTestClient(t, s)
	c.http.Timeout = 5 * time.Millisecond
	c.attempts = 1
	if _, err := c.Read(context.Background()); err == nil {
		t.Fatal("timeout accepted")
	}
}
func TestInvalidPages(t *testing.T) {
	for _, body := range []string{`{}`, `{"result":{"items":[],"total_items":2}}`, `{"result":{"items":[{"product_id":1,"offer_id":"x"},{"product_id":2,"offer_id":"x"}],"total_items":2}}`, `{"result":{"items":[{"product_id":1,"offer_id":"x"}],"total_items":2}}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeTestResponse(t, w, body) }))
			defer s.Close()
			if _, err := newTestClient(t, s).Read(context.Background()); err == nil {
				t.Fatal("invalid page accepted")
			}
		})
	}
}
func TestChangedPageCount(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["last_id"] == "" {
			writeTestResponse(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"x"}],"last_id":"a","total_items":2}}`)
		} else {
			writeTestResponse(t, w, `{"result":{"items":[],"total_items":3}}`)
		}
	}))
	defer s.Close()
	_, err := newTestClient(t, s).Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("count change %v", err)
	}
}

func TestIncompleteStockAndCursor(t *testing.T) {
	for _, body := range []string{
		`{"products":[{"product_id":1,"offer_id":"one","warehouse_id":100,"present":1,"reserved":0}],"has_next":false}`,
		`{"products":[{"product_id":1,"offer_id":"one","warehouse_id":100,"present":1,"reserved":0,"free_stock":1}],"has_next":true,"cursor":""}`,
		`{"products":[{"product_id":1,"offer_id":"one","warehouse_id":100,"present":1,"reserved":0,"free_stock":1},{"product_id":1,"offer_id":"one","warehouse_id":100,"present":1,"reserved":0,"free_stock":1}],"has_next":false}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case productPath:
					var b struct {
						Filter struct {
							Visibility string `json:"visibility"`
						} `json:"filter"`
					}
					_ = json.NewDecoder(r.Body).Decode(&b)
					if b.Filter.Visibility == "ARCHIVED" {
						writeTestResponse(t, w, `{"result":{"items":[],"total_items":0}}`)
					} else {
						writeTestResponse(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"one","sku":2}],"total_items":1}}`)
					}
				case warehousePath:
					writeTestResponse(t, w, `{"warehouses":[],"has_next":false}`)
				case stockPath:
					writeTestResponse(t, w, body)
				default:
					t.Error("unexpected path")
				}
			}))
			defer s.Close()
			if _, err := newTestClient(t, s).Read(context.Background()); err == nil {
				t.Fatal("incomplete stock accepted")
			}
		})
	}
}

func writeTestResponse(t *testing.T, w io.Writer, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Error("write synthetic response failed")
	}
}
