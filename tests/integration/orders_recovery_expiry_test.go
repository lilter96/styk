//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// A short-lived immutable evidence fixture exercises expiry using the database
// clock without altering any historical row or waiting two minutes per test.
func (f ordersRecoveryHTTPFixture) shortEvidence(t *testing.T) orders.RecoveryEvidence {
	t.Helper()
	observed := f.check(t)
	e := *observed.Evidence
	e.ID = ordersProcessID(57001)
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&e.CreatedAt); err != nil {
		t.Fatal(err)
	}
	e.ExpiresAt = e.CreatedAt.Add(3 * time.Second)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.Pool.Exec(f.ctx, `INSERT INTO orders_recovery_evidence(id,work_id,tenant_id,moysklad_account,ozon_account,config_revision,ozon_credential_revision,moysklad_credential_revision,actor,owner_user_id,evidence,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, e.ID, e.WorkID, e.Scope.Tenant, e.Scope.MoyskladAccount, e.Scope.OzonAccount, e.ConfigRevision, e.Credentials.Ozon, e.Credentials.Moysklad, f.session.AuditID, f.session.User.ID, raw, e.CreatedAt, e.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOrdersRecoveryExpiredAttestationCanBeExplicitlyRenewed(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	e := f.shortEvidence(t)
	first := f.resolve(t, e.ID, "recovery-short")
	time.Sleep(time.Until(e.ExpiresAt) + 20*time.Millisecond)
	checked := f.check(t)
	if !checked.Eligible {
		t.Fatalf("expired unused attestation cannot be renewed: %+v", checked.Blockers)
	}
	renewed := f.resolve(t, checked.Evidence.ID, "recovery-renewal")
	if renewed.Resolution.ID != first.Resolution.ID || renewed.Resolution.Attestation.ID == first.Resolution.Attestation.ID {
		t.Fatal("renewal rewrote resolution or reused expired attestation")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_attestations`, 2)
	f.assertOriginal(t)
	preview := f.preview(t)
	if !preview.Decisions[0].Ready {
		t.Fatalf("fresh renewal did not unlock review: %+v", preview.Decisions[0].Validation)
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
}

func TestOrdersRecoveryExpiryBlocksPreviouslyApprovedDispatch(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	e := f.shortEvidence(t)
	f.resolve(t, e.ID, "recovery-expiring-dispatch")
	preview := f.preview(t)
	f.approve(t, preview)
	time.Sleep(time.Until(e.ExpiresAt) + 20*time.Millisecond)
	f.run(t, orders.StateBlocked)
	if f.provider.patches.Load() != 1 {
		t.Fatal("expired operator attestation caused external mutation")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 2)
	checked := f.check(t)
	if !checked.Eligible {
		t.Fatalf("undispatched blocked decision prevents explicit renewal: %+v", checked.Blockers)
	}
	f.resolve(t, checked.Evidence.ID, "recovery-blocked-renewal")
	newer := f.preview(t)
	f.approve(t, newer)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_links`, 2)
	f.assertOriginal(t)
}

func TestOrdersRecoveryNewDispatchedOperationPreventsRenewalOfOldUnknown(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	f.resolve(t, checked.Evidence.ID, "recovery-before-new")
	preview := f.preview(t)
	f.approve(t, preview)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	current := f.check(t)
	if current.Eligible {
		t.Fatal("new UNKNOWN was ignored by old recovery")
	}
	payload := f.body(t, map[string]any{"evidence_id": current.Evidence.ID, "idempotency_key": "recovery-forbidden-renewal", "reason": "Cannot supersede earlier history instead of the new dispatched target", "acknowledge": true, "worker_stopped": true})
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", payload, true); status != 409 {
		t.Fatalf("old recovery renewed after later dispatch HTTP %d", status)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_attestations`, 1)
	f.assertOriginal(t)
}
