package ozon

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/providercall"
)

func TestKnownMethodDenialIsPreservedWithoutReadRetries(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client, err := New(Options{BaseURL: "https://example.invalid", ClientID: "synthetic", APIKey: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 1, HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, providercall.ErrMethodForbidden
	})}})
	if err != nil {
		t.Fatal(err)
	}
	err = client.post(context.Background(), productInfoPath, map[string]any{}, &map[string]any{})
	if !errors.Is(err, providercall.ErrMethodForbidden) || calls.Load() != 1 {
		t.Fatalf("method permission cause was erased or retried: calls=%d err=%v", calls.Load(), err)
	}
}
