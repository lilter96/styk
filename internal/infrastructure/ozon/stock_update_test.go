package ozon

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/testpublication"
)

type stockCleanupBody struct {
	*strings.Reader
	closes int
}

func (b *stockCleanupBody) Close() error {
	b.closes++
	return errors.New("untrusted-cleanup-detail")
}

func TestStockResponseCleanupPreservesOutcome(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	tests := []struct {
		name, body  string
		wantUnknown bool
	}{
		{"acknowledged", `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`, false},
		{"malformed", `{`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := &stockCleanupBody{Reader: strings.NewReader(tc.body)}
			calls := 0
			c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network call") })
			c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
			})
			result, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 1}})
			if errors.Is(err, ErrStockWriteUnknown) != tc.wantUnknown || (!tc.wantUnknown && (err != nil || len(result) != 1 || result[0].State != "SUCCEEDED")) {
				t.Fatalf("cleanup changed write outcome: result=%+v err=%v", result, err)
			}
			if calls != 1 || body.closes != 1 {
				t.Fatalf("calls=%d closes=%d", calls, body.closes)
			}
		})
	}
	if !strings.Contains(logs.String(), "stock response cleanup unavailable") || strings.Contains(logs.String(), "untrusted-cleanup-detail") {
		t.Fatal("cleanup diagnostic missing or contains untrusted detail")
	}
}

func stockTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New(Options{AllowSyntheticStockWrites: true, BaseURL: server.URL, ClientID: "seller", APIKey: "synthetic-private-key", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 5, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestControlledClientNegotiatesHTTP1WithHTTP2CapableServer(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	for _, supportsHTTP2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("server_http2_%v", supportsHTTP2), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.ProtoMajor != 1 || r.URL.Path != stockUpdatePath {
					t.Errorf("unexpected protocol or path: %s %s", r.Proto, r.URL.Path)
				}
				writeTestResponse(t, w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
			}))
			server.EnableHTTP2 = supportsHTTP2
			server.StartTLS()
			t.Cleanup(server.Close)
			c, err := NewControlledTestStockClient(testpublication.Scope(), "synthetic-key")
			if err != nil {
				t.Fatal(err)
			}
			// Only this package test substitutes a local TLS server and trust root.
			c.base, err = url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			transport := c.http.Transport.(*http.Transport)
			pool := x509.NewCertPool()
			pool.AddCert(server.Certificate())
			transport.TLSClientConfig.RootCAs = pool
			item := StockUpdate{testpublication.OzonProduct, testpublication.OzonWarehouse, 1}
			if err = c.BindControlledTestIntent(item, func(context.Context) (bool, error) { return true, nil }); err != nil {
				t.Fatal(err)
			}
			result, err := c.UpdateStocks(context.Background(), []StockUpdate{item})
			if err != nil || calls.Load() != 1 || len(result) != 1 || result[0].State != "SUCCEEDED" {
				t.Fatalf("HTTP/1 negotiation: calls=%d result=%+v err=%v", calls.Load(), result, err)
			}
		})
	}
}

func TestControlledLiveClientIsFixedToOneAccountAndTarget(t *testing.T) {
	scope := testpublication.Scope()
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	if _, err := NewControlledTestStockClient(scope, "synthetic-key"); !errors.Is(err, ErrStockWritesDisabled) {
		t.Fatal("controlled live client must be default-off")
	}
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	wrong := scope
	wrong.OzonAccount = "another-account"
	if _, err := NewControlledTestStockClient(wrong, "synthetic-key"); !errors.Is(err, ErrStockWritesDisabled) {
		t.Fatal("another account was authorized")
	}
	c, err := NewControlledTestStockClient(scope, "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`))}, nil
	})
	for _, items := range [][]StockUpdate{
		{{"another-product", testpublication.OzonWarehouse, 1}},
		{{testpublication.OzonProduct, "another-warehouse", 1}},
		{{testpublication.OzonProduct, testpublication.OzonWarehouse, 8}},
		{{testpublication.OzonProduct, testpublication.OzonWarehouse, -1}},
		{{testpublication.OzonProduct, testpublication.OzonWarehouse, 1}, {testpublication.OzonProduct, testpublication.OzonWarehouse, 0}},
	} {
		if _, err := c.UpdateStocks(context.Background(), items); !errors.Is(err, ErrStockWritesDisabled) {
			t.Fatalf("out-of-scope update accepted: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("an unapproved target reached the transport")
	}
	item := StockUpdate{testpublication.OzonProduct, testpublication.OzonWarehouse, 7}
	if err = c.BindControlledTestIntent(item, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	result, err := c.UpdateStocks(context.Background(), []StockUpdate{item})
	if err != nil || calls.Load() != 1 || len(result) != 1 || result[0].State != "SUCCEEDED" {
		t.Fatalf("approved target: %+v %v", result, err)
	}
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "false")
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{{testpublication.OzonProduct, testpublication.OzonWarehouse, 1}}); !errors.Is(err, ErrStockWritesDisabled) || calls.Load() != 1 {
		t.Fatal("disabled test policy did not stop an existing client")
	}
}

func TestControlledAutomaticIntentIsExactAndSingleUse(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	c, err := NewControlledTestStockClient(testpublication.Scope(), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`))}, nil
	})
	item := StockUpdate{ProductID: "123", WarehouseID: "456", Quantity: 3}
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{item}); !errors.Is(err, ErrStockWritesDisabled) {
		t.Fatal("unbound target was accepted")
	}
	var checks atomic.Int32
	if err = c.BindControlledTestIntent(item, func(context.Context) (bool, error) { checks.Add(1); return true, nil }); err != nil {
		t.Fatal(err)
	}
	wrong := item
	wrong.Quantity++
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{wrong}); !errors.Is(err, ErrStockWritesDisabled) {
		t.Fatal("different quantity was accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.UpdateStocks(context.Background(), []StockUpdate{item}) }()
	}
	wg.Wait()
	if calls.Load() != 1 || checks.Load() != 1 {
		t.Fatalf("expected one authorization and request, got %d and %d", checks.Load(), calls.Load())
	}
}

func TestControlledAutomaticIntentRequiresCurrentAuthorization(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	c, err := NewControlledTestStockClient(testpublication.Scope(), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unexpected") })
	item := StockUpdate{ProductID: "123", WarehouseID: "456", Quantity: 10}
	if err = c.BindControlledTestIntent(item, func(context.Context) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{item}); !errors.Is(err, ErrStockWritesDisabled) || calls.Load() != 0 {
		t.Fatal("revoked authorization reached Ozon")
	}
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{item}); !errors.Is(err, ErrStockWritesDisabled) || calls.Load() != 0 {
		t.Fatal("consumed intent was reused")
	}
}

func TestControlledManualStockRequiresBoundIntent(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	c, err := NewControlledTestStockClient(testpublication.Scope(), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected transport call")
	})
	item := StockUpdate{ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, Quantity: 1}
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{item}); !errors.Is(err, ErrStockWritesDisabled) {
		t.Fatalf("unbound manual write was not rejected: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("unbound manual write reached the transport")
	}
}

type rateWaitContext struct {
	context.Context
	started chan struct{}
	once    sync.Once
}

func (c *rateWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.started) })
	return c.Context.Done()
}

func TestControlledWriteRechecksAuthorizationAfterRateWait(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	tests := []struct {
		name string
		item StockUpdate
	}{
		{"manual", StockUpdate{ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, Quantity: 1}},
		{"automatic", StockUpdate{ProductID: "123", WarehouseID: "456", Quantity: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertAuthorizationAfterRateWait(t, tc.item) })
	}
}

func assertAuthorizationAfterRateWait(t *testing.T, item StockUpdate) {
	t.Helper()
	c, err := NewControlledTestStockClient(testpublication.Scope(), "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected transport call")
	})
	var authorized atomic.Bool
	authorized.Store(true)
	if err = c.BindControlledTestIntent(item, func(context.Context) (bool, error) { return authorized.Load(), nil }); err != nil {
		t.Fatal(err)
	}
	c.next = time.Now().Add(2 * time.Second)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &rateWaitContext{Context: base, started: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, e := c.UpdateStocks(ctx, []StockUpdate{item}); done <- e }()
	select {
	case <-ctx.started:
		authorized.Store(false)
	case <-time.After(time.Second):
		t.Fatal("write did not enter the rate limiter")
	}
	select {
	case err = <-done:
		if !errors.Is(err, ErrStockWritesDisabled) || calls.Load() != 0 {
			t.Fatalf("revoked authorization reached the transport: calls=%d err=%v", calls.Load(), err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("write did not finish")
	}
}

func TestStockWriteSingleAttemptAndAmbiguousResult(t *testing.T) {
	for _, status := range []int{500, 502, 503, 307} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != stockUpdatePath || r.Header.Get("Api-Key") != "synthetic-private-key" {
					t.Error("unexpected scoped request")
				}
				w.WriteHeader(status)
				writeTestResponse(t, w, "synthetic-private-key raw vendor response")
			})
			_, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}})
			if !errors.Is(err, ErrStockWriteUnknown) || calls.Load() != 1 || strings.Contains(err.Error(), "private-key") {
				t.Fatalf("unsafe write retry/result: %d %v", calls.Load(), err)
			}
		})
	}
}
func TestStockWritePartialResultsAndFrozenPayload(t *testing.T) {
	c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stocks []map[string]any `json:"stocks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Stocks) != 2 || body.Stocks[0]["stock"] != float64(0) || body.Stocks[0]["product_id"] != float64(123) || body.Stocks[0]["warehouse_id"] != float64(456) {
			t.Errorf("wrong frozen targets: %#v", body)
		}
		if _, exists := body.Stocks[0]["offer_id"]; exists {
			t.Error("ambiguous product/offer precedence")
		}
		writeTestResponse(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":124,"warehouse_id":456,"updated":false,"errors":[{"code":"PRICE_IS_NOT_SENT","message":"synthetic-private-key"},{"code":"INVALID_ARGUMENT"},{"code":"synthetic-private-key"}]}]}`)
	})
	result, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 0}, {"124", "456", 8}})
	if err != nil || len(result) != 2 || result[0].State != "SUCCEEDED" || result[1].State != "UNKNOWN" || result[1].SafeError != "PRICE_IS_NOT_SENT" {
		t.Fatalf("incorrect partial results: %#v %v", result, err)
	}
	if result[0].Updated == nil || !*result[0].Updated || result[1].Updated == nil || *result[1].Updated || len(result[1].ErrorCodes) != 3 || result[1].ErrorCodes[0] != "PRICE_IS_NOT_SENT" || result[1].ErrorCodes[1] != "INVALID_ARGUMENT" || result[1].ErrorCodes[2] != "MARKETPLACE_REJECTED" {
		t.Fatalf("per-target response lost or exposed unsafe text: %+v", result)
	}
}
func TestStockWriteMissingMalformedOrDuplicateResultsAreUnknown(t *testing.T) {
	for _, body := range []string{`{}`, `{"result":[]}`, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true}]}`, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":123,"warehouse_id":456}]}`, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":999,"warehouse_id":456,"updated":true,"errors":[]}]}`} {
		c := stockTestClient(t, func(w http.ResponseWriter, _ *http.Request) { writeTestResponse(t, w, body) })
		result, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}})
		if err == nil && (len(result) != 1 || result[0].State != "UNKNOWN") {
			t.Fatalf("invalid response became success: %s %#v", body, result)
		}
	}
	if safeStockCode("synthetic-private-key") != "MARKETPLACE_REJECTED" {
		t.Fatal("untrusted error text exposed")
	}
}
func TestStockWriteValidationPrecedesIOAndReadbackIsReadOnly(t *testing.T) {
	var calls atomic.Int32
	c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != stockPath {
			t.Error("reconciliation attempted a write")
		}
		writeTestResponse(t, w, `{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8,"reserved":2,"present":10}],"has_next":false,"cursor":""}`)
	})
	for _, items := range [][]StockUpdate{{}, {{"bad", "456", 8}}, {{"123", "456", -1}}, {{"123", "456", 8}, {"123", "456", 9}}} {
		if _, err := c.UpdateStocks(context.Background(), items); err == nil {
			t.Fatal("invalid stock update accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached provider")
	}
	qty, err := c.ReadStockQuantity(context.Background(), "123", "456", "offer")
	if err != nil || qty != 8 || calls.Load() != 1 {
		t.Fatalf("readback quantity: %d %v", qty, err)
	}
}

func TestStockWritesRequireExplicitLoopbackOptIn(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	opts := Options{BaseURL: server.URL, ClientID: "synthetic-seller", APIKey: "synthetic-secret", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100}
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}}); !errors.Is(err, ErrStockWritesDisabled) {
		t.Fatalf("default must reject write: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("write sent without opt-in")
	}
	opts.AllowSyntheticStockWrites = true
	opts.HTTPClient = &http.Client{}
	if _, err = New(opts); err == nil {
		t.Fatal("custom synthetic write transport could redirect the request")
	}
	opts.HTTPClient = nil
	for _, endpoint := range []string{"https://api-seller.ozon.ru", "http://localhost:1234", "http://127.0.0.1.evil.example:1234"} {
		opts.BaseURL = endpoint
		opts.AllowSyntheticStockWrites = true
		if _, err = New(opts); err == nil {
			t.Fatalf("remote or DNS endpoint allowed: %s", endpoint)
		}
	}
}

func TestStockWriteHTTPErrorRemainsUnknownAfterPossibleApplication(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var applied atomic.Int32
			c := stockTestClient(t, func(w http.ResponseWriter, _ *http.Request) { applied.Add(1); w.WriteHeader(status) })
			result, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}})
			if applied.Load() != 1 || !errors.Is(err, ErrStockWriteUnknown) || result != nil {
				t.Fatalf("possibly applied result treated as definite: calls %d, result %v, error %v", applied.Load(), result, err)
			}
		})
	}
}

func TestStockWriteTimeoutWhileServerContinuesIsUnknown(t *testing.T) {
	var calls, applied atomic.Int32
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := stockTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		applied.Add(1)
		writeTestResponse(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`)
		close(finished)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.UpdateStocks(ctx, []StockUpdate{{"123", "456", 8}}); done <- err }()
	<-started
	if err := <-done; !errors.Is(err, ErrStockWriteUnknown) {
		t.Fatalf("timeout misclassified: %v", err)
	}
	close(release)
	<-finished
	if calls.Load() != 1 || applied.Load() != 1 {
		t.Fatalf("timeout retried or server did not continue: %d %d", calls.Load(), applied.Load())
	}
}

func TestStockReadbackRejectsIncompleteOrAmbiguousPages(t *testing.T) {
	for _, body := range []string{
		`{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8}],"has_next":true,"cursor":"another-page"}`,
		`{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8}]}`,
		`{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8},{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":7}],"has_next":false}`,
	} {
		c := stockTestClient(t, func(w http.ResponseWriter, _ *http.Request) { writeTestResponse(t, w, body) })
		if _, err := c.ReadStockQuantity(context.Background(), "123", "456", "offer"); !errors.Is(err, ErrStockReadbackUnavailable) {
			t.Fatalf("ambiguous page accepted: %s", body)
		}
	}
}

func TestSyntheticStockWriteTransportDoesNotReuseConnections(t *testing.T) {
	var ports []string
	c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		ports = append(ports, r.RemoteAddr)
		writeTestResponse(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`)
	})
	for i := 0; i < 2; i++ {
		if _, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(ports) != 2 || ports[0] == ports[1] {
		t.Fatalf("write connections were reused: %v", ports)
	}
}
