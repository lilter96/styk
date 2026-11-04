//go:build integration

package integration

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
)

func TestOrdersRecoveryUsesCurrentCredentialsAndRejectsLaterRotation(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	f.rotateMoy(t, 2)
	checked := f.check(t)
	if !checked.Eligible || checked.Evidence.Credentials.Moysklad != 2 || checked.Evidence.Target == nil {
		t.Fatalf("recovery did not use current scoped credentials: %+v", checked.Blockers)
	}
	f.rotateMoy(t, 3)
	body := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-rotated", "reason": "The previous evidence must not authorize after credential replacement", "acknowledge": true, "worker_stopped": true})
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true); status != 409 {
		t.Fatalf("credential rotation ignored HTTP %d", status)
	}
	fresh := f.check(t)
	if fresh.Evidence.Credentials.Moysklad != 3 || !fresh.Eligible {
		t.Fatal("fresh credential recovery remained stale")
	}
	f.resolve(t, fresh.Evidence.ID, "recovery-new-credentials")
	f.assertOriginal(t)
}

func (f ordersRecoveryHTTPFixture) rotateMoy(t *testing.T, revision int64) {
	t.Helper()
	resolver, ok := f.factory.Credentials.(*credentials.Resolver)
	if !ok {
		t.Fatal("missing normal credential resolver")
	}
	scope := credentials.Scope{Tenant: f.config.Scope.Tenant, Provider: "moysklad", Account: f.config.Scope.MoyskladAccount}
	cipher, err := resolver.Vault.Encrypt(scope, revision, "synthetic-orders-moysklad-"+scope.Account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, `UPDATE connection_credentials SET revision=$3,ciphertext=$4 WHERE tenant_id=$1 AND provider='moysklad' AND account_id=$2`, scope.Tenant, scope.Account, revision, cipher); err != nil {
		t.Fatal(err)
	}
}

func TestOrdersRecoveryRevokedOwnerCannotAuthorize(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE saas_users SET disabled_at=clock_timestamp() WHERE id=$1`, f.session.User.ID); err != nil {
		t.Fatal(err)
	}
	body := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-revoked", "reason": "A disabled owner must never authorize an external recovery", "acknowledge": true, "worker_stopped": true})
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true); status != 401 {
		t.Fatalf("revoked owner recovery HTTP %d", status)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_resolutions`, 0)
}

func TestOrdersRecoveryMigrationUpgradeRetainsOriginalUnknown(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	dsn := f.db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "54")
	catalogSyncMigrate(t, dsn, "up")
	var original string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT to_jsonb(w)::text FROM orders_work w WHERE id=$1`, f.unknown.ID).Scan(&original); err != nil || original != f.original {
		t.Fatal("UNKNOWN history changed during migration")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_target_fences`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_previews WHERE recorded_at IS NOT NULL`, 0)
	if err := f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal(err)
	}
	checked := f.check(t)
	f.resolve(t, checked.Evidence.ID, "recovery-upgraded")
	// Later migrations may be safely reversible in this fixture. Exercise the
	// recovery migration itself, rather than whichever migration is newest.
	catalogSyncMigrate(t, dsn, "down-to", "57")
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "down")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("refusing to discard order recovery audit")) {
		t.Fatal("destructive recovery rollback was not rejected")
	}
	f.assertOriginal(t)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_resolutions`, 1)
	catalogSyncMigrate(t, dsn, "up")
	if err := f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.assertOriginal(t)
}

func TestOrdersRecoverySecondMerchantCannotReadOrResolveFirstWork(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	mailbox := &testMailbox{tokens: map[string]string{}}
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", mailbox, f.provider.base.server.URL, f.provider.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	other := newMerchantBrowser(t, server.URL)
	other.registerAndLogin(t, mailbox, "recovery-other@example.test")
	path := "/api/orders/recovery?tenant=" + url.QueryEscape(f.config.Scope.Tenant) + "&moysklad_account=" + url.QueryEscape(f.config.Scope.MoyskladAccount) + "&ozon_account=" + url.QueryEscape(f.config.Scope.OzonAccount) + "&work_id=" + url.QueryEscape(f.unknown.ID)
	if status, _ := other.request(t, http.MethodGet, path, "", false); status != 404 {
		t.Fatalf("cross-merchant history HTTP %d", status)
	}
	if status, _ := other.request(t, http.MethodPost, "/api/orders/recovery/check", f.body(t, nil), true); status != 404 {
		t.Fatalf("cross-merchant check HTTP %d", status)
	}
	payload := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-other-owner", "reason": "A different merchant must not accept another owner's uncertainty", "acknowledge": true, "worker_stopped": true})
	if status, _ := other.request(t, http.MethodPost, "/api/orders/recovery/resolve", payload, true); status != 404 {
		t.Fatalf("cross-merchant resolve HTTP %d", status)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_resolutions`, 0)
}
