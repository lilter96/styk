//go:build integration

package integration

import (
	"github.com/lilter96/styk/internal/orders"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func pauseNativeReadbackFixture(t *testing.T, f *nativeMovementHTTPFixture) {
	t.Helper()
	next := f.config
	next.Enabled = false
	next.TestEnabled = false
	if next.NativeDocuments != nil {
		policy := *next.NativeDocuments
		policy.Enabled = false
		next.NativeDocuments = &policy
	}
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: next, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "pause-native-readback"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrdersNativeHTTPUnknownReadbackSurvivesPauseAndConfigRevision(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	preview := f.proposal(t, orders.ActionDemandCreate, false, "native-pause-event", "")
	f.approve(t, preview)
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/entity/demand" {
			recorder := httptest.NewRecorder()
			f.native.serve(recorder, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{}`)); err != nil {
				t.Error(err)
			}
			return
		}
		f.native.serve(w, r)
	})
	f.run(t, orders.StateUnknown)
	original := f.latestWork(t)
	if original.Receipt.State != orders.StateUnknown || original.Receipt.HTTPStatus != http.StatusOK {
		t.Fatal("expected actual HTTP200 with unverified ACK preserved")
	}
	pauseNativeReadbackFixture(t, &f)
	f.run(t, orders.StateReconciled)
	final := f.latestWork(t)
	if final.ID != original.ID || final.ConfigRevision != original.ConfigRevision || !reflect.DeepEqual(final.Receipt, original.Receipt) || final.DispatchCount != 1 || f.native.writes.Load() != 1 {
		t.Fatal("read-only recovery rewrote original intent/receipt or repeated native POST")
	}
	if final.NativeDocument == nil || final.NativeDocument.ID == "" || final.NativeDocument.Kind != "demand" || final.NativeDocument.ObservedAt.IsZero() {
		t.Fatal("verified reconciliation reference missing after HTTP200 UNKNOWN")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_intents`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 1)
}

func TestOrdersNativeHTTPPauseStillBlocksUnsentMutation(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	preview := f.proposal(t, orders.ActionDemandCreate, false, "native-unsent-pause", "")
	f.approve(t, preview)
	pauseNativeReadbackFixture(t, &f)
	f.run(t, orders.StateBlocked)
	if f.native.writes.Load() != 0 || f.latestWork(t).DispatchCount != 0 {
		t.Fatal("read-only pause exception allowed a mutation")
	}
}

func TestOrdersNativeHTTPUnknownReadbackRetainsCredentialAndOwnerBoundaries(t *testing.T) {
	for _, mode := range []string{"credential_rotation", "membership_revocation"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeMovementHTTPFixture(t)
			preview := f.proposal(t, orders.ActionDemandCreate, false, "native-authority-event", "")
			f.approve(t, preview)
			f.native.loseResponse.Store(true)
			f.run(t, orders.StateUnknown)
			if mode == "credential_rotation" {
				if _, err := f.db.Pool.Exec(f.ctx, `UPDATE connection_credentials SET revision=revision+1 WHERE tenant_id=$1 AND provider='moysklad' AND account_id=$2`, f.config.Scope.Tenant, f.config.Scope.MoyskladAccount); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.db.Pool.Exec(f.ctx, `UPDATE saas_memberships SET revoked_at=clock_timestamp() WHERE workspace_id=$1 AND user_id=$2`, f.config.Scope.Tenant, f.session.User.ID); err != nil {
					t.Fatal(err)
				}
			}
			before := f.native.requests.Load()
			runNativeReadbackPhase(t, f)
			if f.native.requests.Load() != before || f.native.writes.Load() != 1 {
				t.Fatal("invalidated read authority reached provider")
			}
		})
	}
}

func runNativeReadbackPhase(t *testing.T, f nativeMovementHTTPFixture) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ran, err := f.runner.RunNext(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ran {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("readback task not claimed")
		}
		time.Sleep(time.Millisecond)
	}
}
