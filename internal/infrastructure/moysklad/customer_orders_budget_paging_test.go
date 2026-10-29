package moysklad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

type pagingCalls struct {
	probes, lists, details, positions atomic.Int32
}

func boundedOrderPagingHandler(t *testing.T, calls *pagingCalls) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unexpected provider mutation")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		respond := func(body any) {
			if err := json.NewEncoder(w).Encode(body); err != nil {
				t.Errorf("write synthetic response: %v", err)
			}
		}
		switch {
		case r.URL.Path == "/entity/product":
			calls.probes.Add(1)
			if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("offset") != "0" {
				t.Error("credential probe exceeded one row")
			}
			respond(map[string]any{"meta": map[string]any{"size": 1}, "rows": []any{map[string]any{"id": "synthetic", "accountId": "account"}}})
		case r.URL.Path == "/entity/customerorder":
			calls.lists.Add(1)
			if r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("offset") != "0" {
				t.Error("bounded order list did not use the multirow page")
			}
			rows := make([]any, 0, 3)
			for i := 1; i <= 3; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("10000000-0000-0000-0000-%012d", i), "accountId": "account"})
			}
			respond(map[string]any{"meta": map[string]any{"size": 3, "limit": 100, "offset": 0}, "rows": rows})
		case strings.HasSuffix(r.URL.Path, "/positions"):
			calls.positions.Add(1)
			if r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("offset") != "0" {
				t.Error("bounded positions did not use the multirow page")
			}
			rows := make([]any, 0, 3)
			for i := 1; i <= 3; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("30000000-0000-0000-0000-%012d", i), "accountId": "account", "quantity": 1, "reserve": 1})
			}
			respond(map[string]any{"meta": map[string]any{"size": 3, "limit": 100, "offset": 0}, "rows": rows})
		case strings.HasPrefix(r.URL.Path, "/entity/customerorder/"):
			calls.details.Add(1)
			respond(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/entity/customerorder/"), "accountId": "account"})
		default:
			t.Error("unexpected provider endpoint")
			http.NotFound(w, r)
		}
	}
}

func TestBoundedOrderPagesUseMultipleRowsWhileProbeRemainsSingle(t *testing.T) {
	t.Parallel()
	calls := &pagingCalls{}
	server := httptest.NewServer(boundedOrderPagingHandler(t, calls))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL + "/", Token: "synthetic", Timeout: time.Second, Interval: time.Microsecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := c.Probe(context.Background(), "account")
	if err != nil || !verified {
		t.Fatalf("probe failed: verified=%t err=%v", verified, err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	batch, counts, err := c.ReadCustomerOrdersBounded(context.Background(), "tenant", "account", now.Add(-time.Hour), now, domain.DefaultMoyskladImportLimits())
	if err != nil || len(batch.Orders) != 3 || counts.Attempts != 7 || counts.Pages != 4 {
		t.Fatalf("unexpected bounded traversal: orders=%d counts=%+v err=%v", len(batch.Orders), counts, err)
	}
	for _, order := range batch.Orders {
		if len(order.Positions) != 3 {
			t.Fatalf("positions missing from multirow page: %d", len(order.Positions))
		}
	}
	if calls.probes.Load() != 1 || calls.lists.Load() != 1 || calls.details.Load() != 3 || calls.positions.Load() != 3 {
		t.Fatalf("unexpected request counts: probes=%d lists=%d details=%d positions=%d", calls.probes.Load(), calls.lists.Load(), calls.details.Load(), calls.positions.Load())
	}
}
