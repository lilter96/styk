//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type nativeCoverageHTTPFixture struct {
	*nativeLifecycleHTTPFixture
	next     http.Handler
	parentID string
	foreign  atomic.Bool
	scans    atomic.Int64
}

func newNativeCoverageHTTPFixture(t *testing.T, presence string) *nativeCoverageHTTPFixture {
	t.Helper()
	f := &nativeCoverageHTTPFixture{nativeLifecycleHTTPFixture: newNativeLifecycleHTTPFixture(t)}
	f.parentID = f.readNative(t).Correlation.CustomerOrderID
	f.demandLinks.Store(presence)
	f.next = f.provider.base.server.Config.Handler
	f.provider.base.server.Config.Handler = f
	return f
}

func (f *nativeCoverageHTTPFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/context/employee" && (r.URL.Path != "/entity/demand" || r.URL.Query().Get("filter") != "") {
		f.next.ServeHTTP(w, r)
		return
	}
	capture := httptest.NewRecorder()
	f.next.ServeHTTP(capture, r)
	var body map[string]any
	decoder := json.NewDecoder(capture.Body)
	decoder.UseNumber()
	if capture.Code != http.StatusOK || decoder.Decode(&body) != nil {
		f.provider.base.t.Error("invalid synthetic native coverage response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if r.URL.Path == "/context/employee" {
		body["permissions"] = map[string]any{"demand": map[string]any{"view": "ALL"}}
		f.provider.base.reply(w, body)
		return
	}
	f.demandPage(w, r, body)
}

func (f *nativeCoverageHTTPFixture) demandPage(w http.ResponseWriter, r *http.Request, body map[string]any) {
	if r.URL.Query().Get("limit") != "1000" || r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("order") != "id,asc" {
		f.provider.base.t.Error("coverage did not use the bounded documented account traversal")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if f.scans.Add(1) == 1 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Millisecond):
		}
	}
	rows := body["rows"].([]any)
	if f.foreign.Load() {
		row := f.provider.base.reference("entity/demand", "demand", ordersProcessID(20999))
		row["id"], row["accountId"], row["applicable"] = ordersProcessID(20999), f.config.Scope.MoyskladAccount, false
		row["customerOrder"] = f.provider.base.reference("entity/customerorder", "customerorder", f.parentID)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].(map[string]any)["id"].(string) < rows[j].(map[string]any)["id"].(string)
	})
	f.provider.base.page(w, rows, len(rows), 1000, 0)
}

func (f *nativeCoverageHTTPFixture) queuedCoverage(t *testing.T) orders.Work {
	t.Helper()
	f.assertNativeIntentQueued(t)
	work := f.latestWork(t)
	coverage := work.Command.NativeMovement.DemandCoverage
	if coverage == nil || coverage.Validation(work.Scope, work.Command.Correlation, work.Command.ExpectedTargetRevision, time.Now()).Code != "" || coverage.AccountRows != 0 || f.scans.Load() != 2 {
		t.Fatal("automatic intent did not freeze complete real HTTP absence evidence")
	}
	if work.Command.CreatedAt.Before(coverage.FinishedAt) || !work.Command.NativeMovement.LinkedDemandIDs.IsMissing() && work.Command.NativeMovement.LinkedDemandIDs.Presence != orders.PresenceNull {
		t.Fatal("late provider evidence was rejected by the old planning clock or rewrote the raw header")
	}
	return work
}

func TestOrdersNativeCoverageHTTPMissingOrNullHeaderUsesCompleteAccountProof(t *testing.T) {
	for _, presence := range []string{"missing", "null"} {
		t.Run(presence, func(t *testing.T) {
			f := newNativeCoverageHTTPFixture(t, presence)
			collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
			f.sellerTransition(t, ordersProcessID(20001))
			queued := f.queuedCoverage(t)
			f.run(t, orders.StateAcknowledged)
			f.runner = f.restartedRunner(t)
			f.run(t, orders.StateReconciled)
			if f.native.writes.Load() != 1 || f.native.shipped.Load() != 1 || f.scans.Load() != 4 || f.latestWork(t).DispatchCount != 1 {
				t.Fatal("full account proof did not yield one physical demand with a fresh preflight scan")
			}
			f.sellerTransition(t, ordersProcessID(20001))
			if worked, err := f.runner.RunNext(f.ctx); err != nil || worked || f.native.writes.Load() != 1 || f.latestWork(t).ID != queued.ID {
				t.Fatal("replayed observation or restarted worker duplicated demand", worked, err)
			}
			f.assertPhysicalStock(t, "9")
		})
	}
}

func TestOrdersNativeCoverageHTTPLateForeignDraftBlocksBeforePost(t *testing.T) {
	f := newNativeCoverageHTTPFixture(t, "missing")
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	f.sellerTransition(t, ordersProcessID(20001))
	f.queuedCoverage(t)
	f.foreign.Store(true)
	f.run(t, orders.StateBlocked)
	work := f.latestWork(t)
	if f.scans.Load() != 4 || f.native.writes.Load() != 0 || f.native.shipped.Load() != 0 || work.DispatchCount != 0 || work.Code != "FINAL_DISPATCH_GUARD_NOT_ENTERED" {
		t.Fatal("late foreign draft bypassed fresh exact-parent coverage", work.Code, f.scans.Load())
	}
	f.assertPhysicalStock(t, "10")
}
