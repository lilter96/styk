//go:build integration

package integration

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersRecoveryConcurrentRequestsReleaseOneFence(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	body := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-concurrent", "reason": "Accept delayed effects for exactly the observed native customer order", "acknowledge": true, "worker_stopped": true})
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true)
			statuses <- status
		})
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 {
			t.Fatalf("idempotent concurrent recovery HTTP %d", status)
		}
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_attestations`, 1)
	f.assertOriginal(t)
	if ran, err := f.runner.RunNext(f.ctx); err != nil || ran {
		t.Fatalf("superseded UNKNOWN readback still claimed: %v %v", ran, err)
	}
}

func TestOrdersRecoveryRequiresExplicitAuthorityAndNoActiveClaim(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	payload := map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-guards", "reason": "Accept exact scoped target uncertainty after stopping prior worker", "acknowledge": true, "worker_stopped": true}
	body := f.body(t, payload)
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, false); status != 403 {
		t.Fatalf("CSRF bypass HTTP %d", status)
	}
	payload["acknowledge"] = false
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", f.body(t, payload), true); status != 400 {
		t.Fatalf("missing acknowledgement HTTP %d", status)
	}
	payload["acknowledge"], payload["tenant"] = true, "other-merchant"
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", f.body(t, payload), true); status != 404 {
		t.Fatalf("cross-tenant recovery HTTP %d", status)
	}
	claimed, err := f.db.ClaimOrderWork(f.ctx, orders.ClaimRequest{WorkerID: "recovery-active-worker", Now: time.Now(), LeaseUntil: time.Now().Add(time.Minute)})
	if err != nil || claimed == nil {
		t.Fatalf("claim unresolved readback: %v", err)
	}
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true); status != 409 {
		t.Fatalf("active worker allowed supersession HTTP %d", status)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_resolutions`, 0)
}

func TestOrdersRecoveryEvidenceStaleAfterSourceChanges(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	f.provider.change(4, 1999)
	f.preview(t)
	body := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-stale", "reason": "Accept exact observed target after fresh source confirmation", "acknowledge": true, "worker_stopped": true})
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true); status != 409 {
		t.Fatalf("stale source recovery HTTP %d", status)
	}
	fresh := f.check(t)
	if !fresh.Eligible {
		t.Fatalf("fresh source did not restore eligibility: %+v", fresh.Blockers)
	}
	f.resolve(t, fresh.Evidence.ID, "recovery-fresh")
	f.assertOriginal(t)
}

func TestOrdersRecoveryAcknowledgementDoesNotAttestContradictoryPositionFrontier(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	f.editForeignReserve(t)
	checked := f.check(t)
	resolved := f.resolve(t, checked.Evidence.ID, "recovery-foreign-change")
	preview := f.preview(t)
	if preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("foreign reserve edit was silently accepted as prior native frontier")
	}
	if resolved.Resolution.Attestation == nil {
		t.Fatal("operator audit lost")
	}
	f.assertOriginal(t)
}

func TestOrdersRecoveryOriginalHistoryCannotBeRewritten(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	f.resolve(t, checked.Evidence.ID, "recovery-history")
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE orders_work SET updated_at=clock_timestamp() WHERE id=$1`, f.unknown.ID); err == nil {
		t.Fatal("historical UNKNOWN was mutable")
	}
	if _, err := f.db.Pool.Exec(f.ctx, `DELETE FROM orders_recovery_resolutions WHERE work_id=$1`, f.unknown.ID); err == nil {
		t.Fatal("immutable resolution was deletable")
	}
	q := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-history", "reason": "Different reason must not reuse an existing recovery key", "acknowledge": true, "worker_stopped": true})
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", q, true); status != 409 {
		t.Fatalf("changed idempotency input HTTP %d", status)
	}
	f.assertOriginal(t)
}

func TestOrdersRecoveryUnsupportedCreateIsDiscoveryOnly(t *testing.T) {
	f := newPositionHTTPFixture(t)
	f.approve(t, f.preview(t))
	work := ordersStoreClaim(t, f.ordersStoreFixture, time.Minute)
	if allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Begin: true}); err != nil || !allowed {
		t.Fatalf("begin create intent: %v %v", allowed, err)
	}
	receipt := orders.Receipt{CommandID: work.Command.ID, State: orders.StateUnknown, Code: "NETWORK_UNKNOWN", ObservedAt: time.Now()}
	completion := orders.Completion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, Code: receipt.Code, Receipt: receipt}
	if err := f.db.CompleteOrderWork(f.ctx, completion); err != nil {
		t.Fatal(err)
	}
	unknown := f.latestWork(t)
	recovery := ordersRecoveryHTTPFixture{positionHTTPFixture: f, unknown: unknown}
	recovery.attachAPI(t)
	checked := recovery.check(t)
	if checked.Eligible || checked.Evidence.Target != nil || checked.Work.State != orders.StateUnknown {
		t.Fatal("zero discovered creates released uncertain creation")
	}
	body := recovery.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "unknown-create-denied", "reason": "No search result cannot prove original creation was unapplied", "acknowledge": true, "worker_stopped": true})
	if status, _ := recovery.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true); status != 409 {
		t.Fatalf("create supersession HTTP %d", status)
	}
	recovery.run(t, orders.StateUnknown)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_target_fences`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_resolutions`, 0)
}

func TestOrdersRecoveryConcurrentDistinctIntentsCannotReleaseTwice(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, key := range []string{"recovery-distinct-a", "recovery-distinct-b"} {
		body := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": key, "reason": "Explicit concurrent recovery of exactly one native target", "acknowledge": true, "worker_stopped": true})
		wg.Go(func() {
			status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true)
			statuses <- status
		})
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("competing recovery outcomes: %v", counts)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_attestations`, 1)
	f.assertOriginal(t)
}
