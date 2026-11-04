//go:build integration

package postgres

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

func inventoryPointQueryFixture(t *testing.T, f orderInventoryFixture, binding orders.Correlation, baseline time.Time) map[int]nativePointInventoryRead {
	t.Helper()
	pool := domain.StockPool{Tenant: binding.Scope.Tenant, MoyskladAccount: binding.Scope.MoyskladAccount, WarehouseID: "native-store", ItemKind: "product", ItemID: "native-product"}
	observations := []orderinventory.Observation{{Binding: binding, Posting: orders.Posting{Scope: binding.Scope, PostingNumber: binding.PostingNumber}}}
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	result, err := readNativePointsForInventory(f.ctx, tx, pool, observations, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInventoryNativePointsPostgresSameSnapshotBeforeBaselineSurvivesPolls(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	first := nativePointReadFixture(t, binding, time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, first, "inventory-point-original")); err != nil {
		t.Fatal(err)
	}
	old := inventoryPointQueryFixture(t, f, binding, time.Time{})[0]
	if old.Point == nil || old.Code != "" {
		t.Fatalf("original point unavailable: %s", old.Code)
	}
	var baseline time.Time
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	// Deliberately older host clocks on the new attempt must not reorder durable
	// admission or turn identical facts into a new source revision.
	second := nativePointReadFixture(t, binding, first.StartedAt.Add(-time.Second))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, second, "inventory-point-late-same")); err != nil {
		t.Fatal(err)
	}
	latest := inventoryPointQueryFixture(t, f, binding, time.Time{})[0]
	chosen := inventoryPointQueryFixture(t, f, binding, baseline)[0]
	if latest.Point == nil || chosen.Point == nil || chosen.Code != "" {
		t.Fatalf("same facts invalidated valid earlier proof: latest=%s chosen=%s", latest.Code, chosen.Code)
	}
	if !latest.AdmittedAt.After(old.AdmittedAt) || !latest.Point.StartedAt.Equal(second.StartedAt) || !chosen.RecordedAt.Equal(old.RecordedAt) || !chosen.Point.StartedAt.Equal(first.StartedAt) {
		t.Fatal("latest admission or eligible pre-baseline read clocks lost")
	}
	if !chosen.RecordedAt.Before(baseline) || !latest.RecordedAt.After(baseline) {
		t.Fatal("fixture did not bracket the baseline")
	}
	assertNativePointOriginalSnapshot(t, f, first)
	assertNativeInventoryPointAfterBaseline(t, f, binding, baseline, second)
}

func assertNativeInventoryPointAfterBaseline(t *testing.T, f orderInventoryFixture, binding orders.Correlation, baseline time.Time, previous orders.NativeOrderPointRead) {
	t.Helper()
	changed := nativePointReadFixture(t, binding, previous.FinishedAt.Add(time.Millisecond))
	changed.Point.TargetRevision = "changed-native-facts"
	changed.Point.Positions[0].Reserve = orders.Present(orders.Decimal("2"))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, changed, "inventory-point-changed")); err != nil {
		t.Fatal(err)
	}
	read := inventoryPointQueryFixture(t, f, binding, baseline)[0]
	if read.Point != nil || read.Code != "NATIVE_POINT_AFTER_BASELINE" {
		t.Fatalf("new facts reused older baseline: %s", read.Code)
	}
	failed := changed
	failed.Point = nil
	failed.Code = "NATIVE_RATE_LIMITED"
	failed.StartedAt, failed.FinishedAt = changed.FinishedAt.Add(time.Millisecond), changed.FinishedAt.Add(2*time.Millisecond)
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, failed, "inventory-point-failure")); err != nil {
		t.Fatal(err)
	}
	for _, cutoff := range []time.Time{baseline, time.Time{}} {
		read = inventoryPointQueryFixture(t, f, binding, cutoff)[0]
		if read.Point != nil || read.Code != "NATIVE_RATE_LIMITED" {
			t.Fatalf("latest failed attempt revived successful history: %s", read.Code)
		}
	}
}

func TestInventoryNativePointsPostgresCredentialAndBindingScope(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	first := nativePointReadFixture(t, binding, time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, first, "inventory-point-scope")); err != nil {
		t.Fatal(err)
	}
	read := inventoryPointQueryFixture(t, f, binding, time.Time{})[0]
	if read.Point == nil {
		t.Fatalf("valid point unavailable: %s", read.Code)
	}
	foreign := binding
	foreign.CustomerOrderID = "foreign-native-order"
	if result := inventoryPointQueryFixture(t, f, foreign, time.Time{}); len(result) != 0 {
		t.Fatal("foreign native identity reused another snapshot")
	}
	foreign = binding
	foreign.Scope.Tenant = "foreign-tenant"
	foreign = inventoryForeignPointBinding(t, foreign)
	if result := inventoryPointQueryFixture(t, f, foreign, time.Time{}); len(result) != 0 {
		t.Fatal("native evidence crossed tenant")
	}
	foreign = binding
	foreign.Scope.MoyskladAccount = "foreign-moy"
	foreign = inventoryForeignPointBinding(t, foreign)
	if result := inventoryPointQueryFixture(t, f, foreign, time.Time{}); len(result) != 0 {
		t.Fatal("native evidence crossed destination account")
	}
	foreign = binding
	foreign.Scope.OzonAccount = "foreign-ozon"
	foreign = inventoryForeignPointBinding(t, foreign)
	if result := inventoryPointQueryFixture(t, f, foreign, time.Time{}); len(result) != 0 {
		t.Fatal("native evidence crossed marketplace account")
	}
	inventoryExec(t, f.db, `UPDATE connection_credentials SET revision=revision+1 WHERE tenant_id=$1 AND provider='moysklad' AND account_id=$2`, binding.Scope.Tenant, binding.Scope.MoyskladAccount)
	read = inventoryPointQueryFixture(t, f, binding, time.Time{})[0]
	if read.Point != nil || read.Code != "NATIVE_CREDENTIAL_REVISION_CHANGED" {
		t.Fatalf("old credential proof reused: %s", read.Code)
	}
	inventoryExec(t, f.db, `UPDATE connection_credentials SET enabled=false,ciphertext=NULL,revision=revision+1 WHERE tenant_id=$1 AND provider='moysklad' AND account_id=$2`, binding.Scope.Tenant, binding.Scope.MoyskladAccount)
	read = inventoryPointQueryFixture(t, f, binding, time.Time{})[0]
	if read.Point != nil || read.Code != "NATIVE_CREDENTIAL_REVISION_CHANGED" {
		t.Fatalf("disabled credential proof reused: %s", read.Code)
	}
}

func inventoryForeignPointBinding(t *testing.T, binding orders.Correlation) orders.Correlation {
	t.Helper()
	correlation, err := orders.CorrelationFor(binding.Scope, binding.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = binding.CustomerOrderID
	return correlation
}
