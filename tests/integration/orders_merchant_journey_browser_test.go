//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// This browser uses normal merchant APIs and nine independent processes. Its
// hidden native response is a provider fault, never a fabricated DB operation.
func TestOrdersMerchantCurrentStateBrowserStack(t *testing.T) {
	if os.Getenv("STYK_PROCESS_E2E") != "true" || os.Getenv("STYK_ORDERS_BROWSER_E2E") != "1" {
		t.Skip("requires isolated process/browser acceptance flags")
	}
	s := newNativeProcessStack(t)
	s.provider.server.Config.Handler = merchantJourneyProvider(s)
	prepareMultiInstanceNativeRoot(t, s)
	s.hidden.Store(true)
	s.native.loseResponse.Store(true)
	s.nativeApprove(t, s.nativePreview(t, "browser-native-unknown"), "browser-native-unknown")
	s.workersEnabled(t, true)
	s.awaitState(t, "position-http-posting", orders.StateUnknown)
	seedMerchantHistoryOutsideLatestPage(t, s)
	key := ordersProcessRandom(t)
	proxy := merchantJourneyProxy(t, s, key)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	ordersProcessPlaywright(t, ctx, proxy.URL, s.a.scope, key, "e2e/orders-current-state-stack.spec.ts")
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
	if s.native.writes.Load() != 1 || s.native.shipped.Load() != 0 || s.native.returned.Load() != 0 {
		t.Fatal("browser current-state recovery repeated native mutation or physical effect")
	}
}

func merchantJourneyProvider(s *nativeProcessStack) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") == s.b.scope.OzonAccount || r.Header.Get("Authorization") == "Bearer synthetic-orders-moysklad-"+s.b.scope.MoyskladAccount {
			s.provider.serve(w, r)
			return
		}
		if r.URL.Path != "/v3/posting/fbs/get" {
			s.serve(w, r)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		var body struct {
			Posting string `json:"posting_number"`
		}
		if json.Unmarshal(raw, &body) != nil {
			w.WriteHeader(400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if body.Posting != "position-http-posting" {
			s.provider.serve(w, r)
			return
		}
		s.serve(w, r)
	})
}

func seedMerchantHistoryOutsideLatestPage(t *testing.T, s *nativeProcessStack) {
	t.Helper()
	numbers := make([]string, 26)
	for i := range numbers {
		numbers[i] = ordersProcessNumber("merchant-history", i)
		s.provider.add(s.a.scope.OzonAccount, numbers[i], "FBS", false)
	}
	s.a.check(t)
	ordersProcessEventually(t, 20*time.Second, "merchant history posting traversal", func() bool {
		var count int
		err := s.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders_posting_heads WHERE tenant_id=$1 AND posting_number=ANY($2::text[])`, s.a.scope.Tenant, numbers).Scan(&count)
		return err == nil && count == len(numbers)
	})
	for _, number := range numbers {
		s.a.approve(t, s.a.preview(t, number))
	}
	for _, number := range numbers {
		s.awaitState(t, number, orders.StateReconciled)
	}
	history, err := s.db.OrderHistory(context.Background(), s.a.scope, orders.PageRequest{Limit: 25})
	if err != nil || history.NextCursor == "" {
		t.Fatal("merchant fixture did not create a real second history page", err)
	}
	for _, op := range history.Operations {
		for _, work := range op.Targets {
			if work.Command.PostingNumber == "position-http-posting" {
				t.Fatal("unknown root remained in latest25; current-state regression not exercised")
			}
		}
	}
}

func merchantJourneyProxy(t *testing.T, s *nativeProcessStack, key string) *httptest.Server {
	t.Helper()
	proxy := ordersProcessProxy(t, s.apis, s.mailbox, key)
	t.Cleanup(proxy.Close)
	original := proxy.Config.Handler
	proxy.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__test/orders/merchant-reconcile" {
			original.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("X-Orders-Test-Control") != key {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s.hidden.Store(false)
		s.native.loseResponse.Store(false)
		w.WriteHeader(http.StatusNoContent)
	})
	return proxy
}
