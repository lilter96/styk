package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogRequestObserverCountsActualReadAttemptsAndRedactsIdentity(t *testing.T) {
	t.Parallel()
	var sent atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if sent.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	events := make([]catalog.RequestEvent, 0, 3)
	ctx := catalog.WithRequestObserver(context.Background(), func(event catalog.RequestEvent) { events = append(events, event) })
	_, err := client(t, s).get(ctx, "entity/product/"+catalogTestProduct, url.Values{"filter": {"private-query-value"}})
	if err != nil || sent.Load() != 3 || len(events) != 3 {
		t.Fatalf("read dispatch accounting differs: attempts=%d events=%d error=%v", sent.Load(), len(events), err)
	}
	for i, event := range events {
		status := http.StatusInternalServerError
		if i == 2 {
			status = http.StatusOK
		}
		if event != (catalog.RequestEvent{Provider: catalog.ProviderMoySklad, Method: http.MethodGet, Operation: "entity/product/{id}", Status: status}) {
			t.Fatalf("unexpected safe request event: %+v", event)
		}
	}
	assertCatalogObserverRedacted(t, events)
}

type catalogObserverTransport func(*http.Request) (*http.Response, error)

func (transport catalogObserverTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestCatalogRequestObserverCountsTransportErrorsWithoutRawErrors(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	httpClient := &http.Client{Transport: catalogObserverTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("private-transport-error")
	})}
	c, err := New(Options{BaseURL: "http://127.0.0.1/", Token: "test-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 1, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]catalog.RequestEvent, 0, 3)
	ctx := catalog.WithRequestObserver(context.Background(), func(event catalog.RequestEvent) { events = append(events, event) })
	if _, err := c.get(ctx, "entity/variant/"+catalogTestVariant, nil); !errors.Is(err, ErrTransport) {
		t.Fatalf("expected interrupted transport: %v", err)
	}
	if calls.Load() != 3 || len(events) != 3 {
		t.Fatalf("transport attempt accounting differs: dispatches=%d events=%d", calls.Load(), len(events))
	}
	for _, event := range events {
		if event != (catalog.RequestEvent{Provider: catalog.ProviderMoySklad, Method: http.MethodGet, Operation: "entity/variant/{id}", Interrupted: true}) {
			t.Fatalf("unsafe or incomplete transport event: %+v", event)
		}
	}
	assertCatalogObserverRedacted(t, events)
}

func TestCatalogRequestObserverEmitsOneMutationIncludingUnknownOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "500", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			s := newCatalogSimulator(t)
			s.mode = mode
			c, cmd := s.writer(t), catalogCreateCommand("product")
			catalogBind(t, c, cmd, catalogAuthorize)
			events := make([]catalog.RequestEvent, 0, 3)
			ctx := catalog.WithRequestObserver(context.Background(), func(event catalog.RequestEvent) { events = append(events, event) })
			_, err := c.WriteCatalog(ctx, cmd)
			if (mode == "") != (err == nil) || len(events) != 3 {
				t.Fatalf("write accounting differs: events=%d error=%v", len(events), err)
			}
			want := catalog.RequestEvent{Provider: catalog.ProviderMoySklad, Method: http.MethodPost, Operation: "entity/product", Status: http.StatusOK}
			if mode == "500" {
				want.Status = http.StatusInternalServerError
			}
			if mode == "disconnect" {
				want.Status, want.Interrupted = 0, true
			}
			if events[2] != want {
				t.Fatalf("write event differs: got=%+v want=%+v", events[2], want)
			}
			if _, err := c.WriteCatalog(ctx, cmd); !errors.Is(err, ErrCatalogWritesDisabled) || len(events) != 3 {
				t.Fatal("a locally rejected resend emitted a request event")
			}
			assertCatalogObserverRedacted(t, events)
		})
	}
}

func TestCatalogRequestObserverEmitsNothingBeforeHTTPDispatch(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	var count int
	ctx, cancel := context.WithCancel(catalog.WithRequestObserver(context.Background(), func(catalog.RequestEvent) { count++ }))
	cancel()
	if _, err := s.client.get(ctx, "entity/product", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled read did not stop before dispatch")
	}
	if _, err := s.client.get(ctx, "entity/paymentin", nil); err == nil {
		t.Fatal("forbidden endpoint was admitted")
	}
	if count != 0 {
		t.Fatal("a local cancellation or allowlist rejection was counted as an HTTP request")
	}
}

func assertCatalogObserverRedacted(t *testing.T, events []catalog.RequestEvent) {
	t.Helper()
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{catalogTestProduct, catalogTestVariant, "private-query-value", "private-transport-error", "test-token", "http://", "https://", "?"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("request observation exposed a private field: %s", private)
		}
	}
}
