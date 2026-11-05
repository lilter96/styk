//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// This adds no surrogate worker: all nine replicas are independent executable
// processes using the ordinary API, durable runner, scoped adapters and vault.
func TestOrdersMultiInstanceV11DeletedNativeDoesNotReplayOrBlockAnotherTenant(t *testing.T) {
	if os.Getenv("STYK_PROCESS_E2E") != "true" {
		t.Skip("set STYK_PROCESS_E2E=true for independent native deletion recovery")
	}
	s := newNativeProcessStack(t)
	prepareMultiInstanceNativeRoot(t, s)
	var deleted atomic.Bool
	deleted.Store(true)
	s.provider.server.Config.Handler = multiInstanceDeletedNativeHandler(s, &deleted)
	s.nativeApprove(t, s.nativePreview(t, "process-v11-deleted-draft"), "process-v11-deleted-draft")
	s.workersEnabled(t, true)
	awaitDeletedNativeFence(t, s)
	worker := s.workers[0]
	worker.stop(t, true)
	s.workers[0] = s.nativeWorker(t, 0)
	s.workers[0].configure(t, true, "", true)
	assertDeletedNativeOtherTenantProgress(t, s)
	deleted.Store(false)
	clearMultiInstanceNativeDeletion(s)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
	if s.native.writes.Load() != 1 || s.native.shipped.Load() != 0 || s.native.returned.Load() != 0 {
		t.Fatal("deletion recovery repeated a write or invented physical movement")
	}
	t.Logf("native deletion recovery: native_POSTs=%d physical_shipped=%d physical_returned=%d; restarted worker and reconciled by readback", s.native.writes.Load(), s.native.shipped.Load(), s.native.returned.Load())
}

func multiInstanceDeletedNativeHandler(s *nativeProcessStack, deleted *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") == s.b.scope.OzonAccount || r.Header.Get("Authorization") == "Bearer synthetic-orders-moysklad-"+s.b.scope.MoyskladAccount {
			s.provider.serve(w, r)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/entity/demand" || !deleted.Load() {
			s.serve(w, r)
			return
		}
		response := httptest.NewRecorder()
		s.serve(response, r)
		s.native.mu.Lock()
		for _, document := range s.native.documents {
			document.body["deleted"] = "2026-11-05 12:00:00.000"
		}
		s.native.mu.Unlock()
		for key, values := range response.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(response.Code)
		if _, err := w.Write(response.Body.Bytes()); err != nil {
			s.owner.Error("forward synthetic native acknowledgement")
		}
	})
}

func awaitDeletedNativeFence(t *testing.T, s *nativeProcessStack) {
	t.Helper()
	ordersProcessEventually(t, 20*time.Second, "deleted native diagnostic across workers", func() bool {
		var state, code string
		var fenced bool
		err := s.db.Pool.QueryRow(context.Background(), `SELECT w.state,COALESCE(w.work->>'code',''),EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id) FROM orders_work w WHERE w.tenant_id=$1 AND w.posting_number='position-http-posting' AND w.action='demand_create' ORDER BY w.created_at DESC LIMIT 1`, s.a.scope.Tenant).Scan(&state, &code, &fenced)
		return err == nil && state == string(orders.StatePolling) && code == "NATIVE_DOCUMENT_DELETED_REQUIRES_REVIEW" && fenced
	})
	if s.native.writes.Load() != 1 {
		t.Fatal("deleted native intent was dispatched more than once")
	}
}

func assertDeletedNativeOtherTenantProgress(t *testing.T, s *nativeProcessStack) {
	t.Helper()
	number := "independent-v11-other-tenant"
	s.add(t, s.b, number, "FBS", false)
	preview := s.b.preview(t, number)
	status, _ := s.b.request(t, 2, http.MethodPost, "/api/orders/approve", ordersProcessApprovalBody(s.b.scope, preview.ID), true)
	if status != http.StatusOK {
		t.Fatalf("independent tenant approval HTTP %d", status)
	}
	s.awaitState(t, number, orders.StateReconciled)
	awaitDeletedNativeFence(t, s)
	assertDeletedNativeVisibleAcrossAPIs(t, s)
	if s.native.writes.Load() != 1 {
		t.Fatal("independent tenant progress replayed deleted native creation")
	}
}

func prepareMultiInstanceNativeRoot(t *testing.T, s *nativeProcessStack) {
	t.Helper()
	pids := make(map[int]bool)
	for _, group := range [][]*ordersProcessChild{s.apis, s.schedules, s.workers} {
		for _, child := range group {
			pids[child.cmd.Process.Pid] = true
		}
	}
	if len(pids) != 9 {
		t.Fatal("deletion recovery requires nine distinct OS processes")
	}
	t.Logf("native deletion topology: APIs=%d schedulers=%d workers=%d independent_PIDs=%v tenant_A=%s tenant_B=%s", len(s.apis), len(s.schedules), len(s.workers), pids, s.a.scope.Tenant, s.b.scope.Tenant)
	s.add(t, s.a, "position-http-posting", "FBS", false)
	s.approveAcrossReplicas(t, s.a.preview(t, "position-http-posting"))
	s.workersEnabled(t, true)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
}

func clearMultiInstanceNativeDeletion(s *nativeProcessStack) {
	s.native.mu.Lock()
	defer s.native.mu.Unlock()
	for _, document := range s.native.documents {
		delete(document.body, "deleted")
	}
}

func assertDeletedNativeVisibleAcrossAPIs(t *testing.T, s *nativeProcessStack) {
	t.Helper()
	for replica := range s.apis {
		status, raw := s.a.request(t, replica, http.MethodGet, "/api/orders/operations"+ordersHTTPQuery(s.a.scope), nil, false)
		var history orders.HistoryPage
		if status != http.StatusOK || json.Unmarshal(raw, &history) != nil {
			t.Fatal("native deletion history unavailable across replicas")
		}
		found := false
		for _, operation := range history.Operations {
			for _, work := range operation.Targets {
				found = found || work.Command.Action == orders.ActionDemandCreate && work.Code == "NATIVE_DOCUMENT_DELETED_REQUIRES_REVIEW" && work.DispatchCount == 1
			}
		}
		if !found {
			t.Fatal("API replica hid native deletion uncertainty")
		}
	}
	status, _ := s.b.request(t, 1, http.MethodGet, "/api/orders/operations"+ordersHTTPQuery(s.a.scope), nil, false)
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatal("other tenant accessed native deletion history")
	}
}
