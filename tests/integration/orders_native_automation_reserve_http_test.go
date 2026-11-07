//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (f *nativeLifecycleHTTPFixture) reserveResponse(w http.ResponseWriter, r *http.Request) bool {
	if !f.reserveEffects.Load() || !strings.HasPrefix(r.URL.Path, "/report/stock/") && !(strings.HasPrefix(r.URL.Path, "/entity/customerorder/") && strings.HasSuffix(r.URL.Path, "/positions")) {
		return false
	}
	capture := httptest.NewRecorder()
	f.native.serve(capture, r)
	var body map[string]any
	decoder := json.NewDecoder(capture.Body)
	decoder.UseNumber()
	if capture.Code != http.StatusOK || decoder.Decode(&body) != nil {
		w.WriteHeader(capture.Code)
		return true
	}
	reserve := int64(0)
	if f.native.shipped.Load() == 0 || f.retainReserve.Load() {
		reserve = 1
	}
	f.withReserveRows(body, r.URL.Path, reserve)
	f.provider.base.reply(w, body)
	return true
}

func (f *nativeLifecycleHTTPFixture) withReserveRows(body map[string]any, path string, reserve int64) {
	for _, item := range body["rows"].([]any) {
		row := item.(map[string]any)
		value := json.Number(strconv.FormatInt(reserve, 10))
		switch path {
		case "/report/stock/all":
			row["reserve"] = value
			row["freeStock"] = json.Number(strconv.FormatInt(10-f.native.shipped.Load()+f.native.returned.Load()-reserve, 10))
		case "/report/stock/bystore":
			for _, warehouse := range row["stockByStore"].([]any) {
				warehouse.(map[string]any)["reserve"] = value
			}
		default:
			if row["id"] == ordersProcessID(999) {
				row["reserve"] = value
			}
		}
	}
}

func (f *nativeLifecycleHTTPFixture) enableOpenReserve(t *testing.T) {
	t.Helper()
	f.config.FieldMappings = slices.DeleteFunc(f.config.FieldMappings, func(m orders.FieldMapping) bool { return m.Target == "positions.reserve" })
	f.config.FieldMappings = append(f.config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"})
	saved, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "native-lifecycle-open-reserve"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.config = saved
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.reserveEffects.Store(true)
}

func (f *nativeLifecycleHTTPFixture) assertStockAndReserve(t *testing.T, physical, reserve, available string) {
	t.Helper()
	batch := f.stockImport(t)
	var observedPhysical, observedReserve, observedAvailable string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT stock::text,reserve::text,(stock-reserve)::text FROM source_warehouse_stock_observations WHERE import_id=$1 AND external_id=$2 AND warehouse_id=$3`, batch, ordersProcessID(8), f.config.StoreID).Scan(&observedPhysical, &observedReserve, &observedAvailable); err != nil || observedPhysical != physical || observedReserve != reserve || observedAvailable != available {
		t.Fatal("actual provider inventory outcome", observedPhysical, observedReserve, observedAvailable, err)
	}
}

func (f *nativeLifecycleHTTPFixture) assertNativeIntentQueued(t *testing.T) {
	t.Helper()
	items, err := f.db.OrderMerchantIntents(f.ctx, f.config.Scope, 10)
	if err != nil || len(items) != 1 || items[0].State != "QUEUED" {
		t.Fatal("physical native intent was not queued", items, err)
	}
}

func (f *nativeLifecycleHTTPFixture) sellerTransitionThroughChanges(t *testing.T, state string) {
	t.Helper()
	f.state.Store(&nativeLifecycleHTTPState{ID: state, Updated: time.Now().Add(-3 * time.Second).In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05.000")})
	collectNativeLifecycleChanges(t, f)
	if err := f.service.ProcessMerchantIntents(f.ctx, f.config); err != nil {
		t.Fatal(err)
	}
	f.assertNativeIntentQueued(t)
}

func TestOrdersNativeAutomaticHTTPReserveBecomesOnePhysicalMovement(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	f.enableOpenReserve(t)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	f.assertStockAndReserve(t, "10", "1", "9")
	f.sellerTransitionThroughChanges(t, ordersProcessID(20001))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.assertStockAndReserve(t, "9", "0", "9")
	if f.native.writes.Load() != 1 {
		t.Fatal("reservation and physical shipment caused more than one native document")
	}
	parent := f.readNative(t)
	for _, position := range parent.Positions {
		if position.ID == ordersProcessID(999) && (!position.Reserve.IsKnown() || *position.Reserve.Value != "0" || !position.Shipped.IsKnown() || *position.Shipped.Value != "1") {
			t.Fatal("linked applied demand did not show exact native reserve/shipped effect")
		}
	}
}

func TestOrdersNativeAutomaticHTTPMissingReserveReleaseRemainsUnconfirmed(t *testing.T) {
	f := newNativeLifecycleHTTPFixture(t)
	f.enableOpenReserve(t)
	f.retainReserve.Store(true)
	collectNativeAttributionPoint(t, f.nativeMovementHTTPFixture)
	f.sellerTransitionThroughChanges(t, ordersProcessID(20001))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StatePolling)
	f.assertStockAndReserve(t, "9", "1", "8")
	if f.native.writes.Load() != 1 || f.latestWork(t).Code != "NATIVE_PARENT_FACTS_NOT_CONFIRMED" {
		t.Fatal("unreleased provider reserve became reconciled or was silently cleared")
	}
}
