//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersOpenQuantityReserveUsesNormalHTTPWorkerAndOwnedRelease(t *testing.T) {
	f := newPositionHTTPFixture(t)
	config := f.config
	config.FieldMappings = slices.DeleteFunc(config.FieldMappings, func(mapping orders.FieldMapping) bool {
		return mapping.Target == "positions.reserve" || mapping.Target == "applicable"
	})
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}, orders.FieldMapping{Source: "posting.cancelled", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "positive-reserve-policy"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var cancelled atomic.Bool
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/posting/fbs/get" || !cancelled.Load() {
			f.provider.serve(w, r)
			return
		}
		row := f.provider.base.posting("position-http-posting", "FBS")
		row["status"] = "cancelled"
		row["products"].([]any)[0].(map[string]any)["quantity"] = f.provider.quantity.Load()
		row["updated_at"] = time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC).Add(time.Duration(f.provider.revision.Load()) * time.Minute).Format(time.RFC3339)
		f.provider.base.reply(w, map[string]any{"result": row})
	})
	first := f.preview(t)
	if !first.Decisions[0].Ready || len(first.Decisions[0].Commands) != 1 {
		t.Fatalf("positive reserve preview blocked: %+v", first.Decisions[0].Validation)
	}
	firstWrite := first.Decisions[0].Commands[0].CustomerOrder
	if !firstWrite.Applicable.IsKnown() || !*firstWrite.Applicable.Value || !firstWrite.Positions[0].Reserve.IsKnown() || *firstWrite.Positions[0].Reserve.Value != "2" {
		t.Fatal("explicit posted reserve policy lost")
	}
	f.approve(t, first)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertReservePolicyNative(t, f, "2")
	f.addForeignLine(t)
	f.provider.change(1, 1234)
	next := f.preview(t)
	if !next.Decisions[0].Ready || len(next.Decisions[0].Commands) != 1 {
		t.Fatalf("owned reserve adjustment blocked: %+v", next.Decisions[0].Validation)
	}
	patch := next.Decisions[0].Commands[0].PositionPatch
	if patch == nil || !patch.Quantity.IsKnown() || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "1" {
		t.Fatal("quantity and owned reserve must change in same exact intent")
	}
	f.approve(t, next)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertReservePolicyNative(t, f, "1")
	noop := f.preview(t)
	if !noop.Decisions[0].Ready || len(noop.Decisions[0].Commands) != 0 {
		t.Fatal("unchanged reserve policy generated a duplicate mutation")
	}
	cancelled.Store(true)
	f.provider.change(1, 1234)
	release := f.preview(t)
	if !release.Decisions[0].Ready || len(release.Decisions[0].Commands) != 1 {
		t.Fatalf("owned cancellation release blocked: %+v", release.Decisions[0].Validation)
	}
	patch = release.Decisions[0].Commands[0].PositionPatch
	if patch == nil || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "0" {
		t.Fatal("cancellation did not target owned reserve")
	}
	f.approve(t, release)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertReservePolicyNative(t, f, "0")
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE state='RECONCILED' AND dispatch_count=1`, 3)
}

func assertReservePolicyNative(t *testing.T, f positionHTTPFixture, reserve string) {
	t.Helper()
	target := f.readNative(t)
	if !target.Complete || len(target.Positions) == 0 {
		t.Fatal("complete native order observation missing")
	}
	for _, position := range target.Positions {
		if position.ID == ordersProcessID(999) {
			if !position.Reserve.IsKnown() || string(*position.Reserve.Value) != reserve {
				t.Fatalf("owned reserve mismatch: wanted %s", reserve)
			}
			continue
		}
		if !position.Reserve.IsKnown() || *position.Reserve.Value != "2" {
			t.Fatal("foreign reserve changed")
		}
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if reserve != "2" && f.provider.lastPatch["reserve"] != json.Number(reserve) {
		t.Fatalf("reserve not sent through actual native position API: %s", reserve)
	}
}
