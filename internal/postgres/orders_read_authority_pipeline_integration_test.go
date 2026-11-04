//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func claimReadAuthorityLease(t *testing.T, f orderInventoryFixture) *orders.IngestionLease {
	t.Helper()
	now := time.Now()
	lease, err := f.db.ClaimOrderIngestion(context.Background(), orders.IngestionClaimRequest{Scope: f.config.Scope, Stream: orders.NativePointStream, RefreshKey: "read-authority-native-epoch", WorkerID: "background-native-reader", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatal("claim authorized native evidence read without browser session", err)
	}
	return lease
}

func authorityNativePage(lease orders.IngestionLease) orders.SourcePage {
	now := time.Now().UTC()
	return orders.SourcePage{Scope: lease.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: lease.Stream, RefreshKey: lease.RefreshKey, WindowFrom: now.Add(-time.Hour), WindowTo: now, StartedAt: now, FinishedAt: now, Complete: true, Exhausted: true, FreshDetail: true, IdempotencyKey: "authority-native-page", FenceToken: lease.Token}
}

func authorityNativePostingPage(t *testing.T, f orderInventoryFixture, lease orders.IngestionLease) orders.SourcePage {
	t.Helper()
	posting := orderInventoryPosting(t, f, "authority-native-posting", "awaiting_packaging")
	binding, err := readNativePointBinding(f.ctx, f.db.Pool, f.config.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	binding.CustomerOrderID = "authority-native-order"
	tx, err := f.db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if err = resolveOrderProjection(context.Background(), tx, binding, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	page := authorityNativePage(lease)
	target := orders.TargetObservation{Scope: binding.Scope, Correlation: binding, Revision: "authority-native-revision", Complete: true, ObservedAt: page.StartedAt, Fields: orders.CustomerOrderWrite{StoreID: f.config.StoreID, Applicable: orders.Present(true)}, Positions: []orders.TargetPosition{{ID: "authority-position", MoyskladKind: "product", MoyskladID: "native-product", Quantity: "3", Reserve: orders.Present(orders.Decimal("3")), Shipped: orders.Present(orders.Decimal("0"))}}}
	point, err := orders.NewNativeOrderPoint(target, lease.Credentials.Moysklad, page.StartedAt, page.FinishedAt)
	if err != nil {
		t.Fatal(err)
	}
	page.NativeReads = []orders.NativeOrderPointRead{{Correlation: binding, CredentialRevision: point.CredentialRevision, StartedAt: point.StartedAt, FinishedAt: point.FinishedAt, Point: &point}}
	return page
}

func TestOrderReadAuthorityNativeClaimAndCommitSurviveLogoutAndSessionExpiry(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE saas_sessions SET revoked_at=clock_timestamp()`,
		`UPDATE saas_sessions SET expires_at=created_at+interval '1 microsecond'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f := orderReadAuthorityFixture(t, false)
			inventoryExec(t, f.db, mutation)
			lease := claimReadAuthorityLease(t, f)
			if renewed, err := f.db.RenewOrderIngestion(context.Background(), *lease, time.Now().Add(2*time.Minute)); err != nil || !renewed {
				t.Fatal("browser session ended valid background read permission", err)
			}
			checkpoint, err := f.db.CommitOrderPage(context.Background(), authorityNativePostingPage(t, f, *lease))
			if err != nil || !checkpoint.Complete || checkpoint.Revision != 1 {
				t.Fatal("authorized native enumeration did not persist without browser cookie", err)
			}
			var reads, snapshots, fullBatches int
			if err = f.db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM orders_native_point_reads),(SELECT count(*) FROM orders_native_point_snapshots),(SELECT count(*) FROM moy_order_batches)`).Scan(&reads, &snapshots, &fullBatches); err != nil || reads != 1 || snapshots != 1 || fullBatches != 0 {
				t.Fatal("point evidence missing or misrepresented as full account traversal", err)
			}
		})
	}
}

func TestOrderReadAuthorityRevocationBeforeClaimCreatesNoLease(t *testing.T) {
	f := orderReadAuthorityFixture(t, false)
	inventoryExec(t, f.db, `UPDATE saas_users SET disabled_at=clock_timestamp()`)
	now := time.Now()
	lease, err := f.db.ClaimOrderIngestion(context.Background(), orders.IngestionClaimRequest{Scope: f.config.Scope, Stream: orders.NativePointStream, RefreshKey: "revoked-epoch", WorkerID: "revoked-owner-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
	if lease != nil || !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("disabled owner acquired a new provider read lease", err)
	}
	assertOrderReadAuthorityNoProgress(t, f)
	var leases int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders_ingestion_leases`).Scan(&leases); err != nil || leases != 0 {
		t.Fatal("failed authorization persisted a read lease", err)
	}
}

func TestOrderReadAuthorityMidReadRevocationRollsBackCommitAndRenewal(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE saas_users SET disabled_at=clock_timestamp()`,
		`UPDATE saas_memberships SET revoked_at=clock_timestamp()`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f := orderReadAuthorityFixture(t, false)
			lease := claimReadAuthorityLease(t, f)
			page := authorityNativePostingPage(t, f, *lease)
			inventoryExec(t, f.db, mutation)
			if checkpoint, err := f.db.CommitOrderPage(context.Background(), page); !errors.Is(err, orders.ErrBlocked) || checkpoint.Revision != 0 {
				t.Fatal("owner revocation did not atomically reject read results", err)
			}
			if renewed, err := f.db.RenewOrderIngestion(context.Background(), *lease, time.Now().Add(2*time.Minute)); !errors.Is(err, orders.ErrBlocked) || renewed {
				t.Fatal("revoked owner renewed pending provider reads", err)
			}
			assertOrderReadAuthorityNoProgress(t, f)
			assertOrderReadLeaseUnchanged(t, f, *lease)
		})
	}
}

func TestOrderReadAuthorityMidReadConfigDisableRejectsOldLease(t *testing.T) {
	f := orderReadAuthorityFixture(t, false)
	lease := claimReadAuthorityLease(t, f)
	page := authorityNativePostingPage(t, f, *lease)
	config := f.config
	config.Enabled = false
	if _, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: "user.aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "disable-after-read-claim"}, time.Now()); err != nil {
		t.Fatal("disable account read permission", err)
	}
	if _, err := f.db.CommitOrderPage(context.Background(), page); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("old read lease persisted after disable", err)
	}
	if renewed, err := f.db.RenewOrderIngestion(context.Background(), *lease, time.Now().Add(2*time.Minute)); renewed || err == nil {
		t.Fatal("disabled configuration renewed old read lease", err)
	}
	assertOrderReadAuthorityNoProgress(t, f)
	assertOrderReadLeaseUnchanged(t, f, *lease)
}

func assertOrderReadAuthorityNoProgress(t *testing.T, f orderInventoryFixture) {
	t.Helper()
	for _, table := range []string{"orders_ingestion_pages", "orders_checkpoints", "orders_native_point_reads", "orders_native_point_snapshots"} {
		var count int
		if err := f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("unauthorized results or progress escaped rollback", table, err)
		}
	}
}

func assertOrderReadLeaseUnchanged(t *testing.T, f orderInventoryFixture, lease orders.IngestionLease) {
	t.Helper()
	var until time.Time
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT lease_until FROM orders_ingestion_leases WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND token=$4`, lease.Scope.Tenant, lease.Scope.MoyskladAccount, lease.Scope.OzonAccount, lease.Token).Scan(&until); err != nil || !until.Equal(lease.LeaseUntil) {
		t.Fatal("revoked permission extended or reassigned its lease", err)
	}
}
