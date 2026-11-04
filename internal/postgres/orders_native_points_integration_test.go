//go:build integration

package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func nativePointTestDB(t *testing.T) *Store {
	t.Helper()
	for _, key := range []string{"MOYSKLAD_TOKEN", "OZON_API_KEY", "OZON_CLIENT_ID", "MOYSKLAD_ACCOUNT_ID"} {
		if os.Getenv(key) != "" {
			t.Fatal("native point tests require blank live provider credentials")
		}
	}
	u, err := nativePointTestURL(os.Getenv("ORDERS_POINT_TEST_DATABASE_URL"), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	return orderInventoryIsolatedSchema(t, u)
}

func nativePointStoreFixture(t *testing.T) (orderInventoryFixture, orders.Correlation) {
	t.Helper()
	db, now := nativePointTestDB(t), time.Now().UTC().Truncate(time.Microsecond)
	scope := orders.Scope{Tenant: "point-tenant", MoyskladAccount: "point-moy", OzonAccount: "point-ozon"}
	actor := orderInventoryOwner(t, db, scope.Tenant)
	ctx := identity.WithSession(context.Background(), actor)
	for _, connection := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, scope.Tenant, connection.provider, connection.account, "synthetic native point", true); err != nil {
			t.Fatal(err)
		}
		inventoryExec(t, db, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'Synthetic',1,$4,clock_timestamp(),'point-fixture')`, scope.Tenant, connection.provider, connection.account, make([]byte, 29))
	}
	config, err := db.SaveOrderConfig(ctx, orders.SaveConfigRequest{Config: orders.Config{Scope: scope, Enabled: true, IntervalSeconds: 300, SourceFreshnessSeconds: 3600, PreviewTTLSeconds: 300, OrganizationID: "org", AgentID: "agent", StoreID: "native-store", StateID: "native-state", SalesChannelID: "native-channel"}, Actor: actor.AuditID, IdempotencyKey: "point-config"}, now)
	if err != nil {
		t.Fatal(err)
	}
	f := orderInventoryFixture{db: db, ctx: ctx, config: config, now: now}
	posting := orderInventoryPosting(t, f, "point-posting", "cancelled")
	binding, err := readNativePointBinding(ctx, db.Pool, scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	binding.CustomerOrderID = "existing-native-order"
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if err = resolveOrderProjection(ctx, tx, binding, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return f, binding
}

func nativePointReadFixture(t *testing.T, binding orders.Correlation, at time.Time) orders.NativeOrderPointRead {
	t.Helper()
	target := orders.TargetObservation{Scope: binding.Scope, Correlation: binding, Revision: "same-native-facts", Complete: true, ObservedAt: at, Fields: orders.CustomerOrderWrite{StoreID: "native-store", Applicable: orders.Present(true)}, Positions: []orders.TargetPosition{{ID: "native-position", MoyskladKind: "product", MoyskladID: "native-product", Quantity: "3", Reserve: orders.Field[orders.Decimal]{Presence: orders.PresenceNull}, Shipped: orders.Present(orders.Decimal("0"))}}}
	point, err := orders.NewNativeOrderPoint(target, 1, at.Add(-time.Millisecond), at.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return orders.NativeOrderPointRead{Correlation: binding, CredentialRevision: 1, StartedAt: point.StartedAt, FinishedAt: point.FinishedAt, Point: &point}
}

func nativePointPage(t *testing.T, f orderInventoryFixture, read orders.NativeOrderPointRead, key string) orders.SourcePage {
	t.Helper()
	previous, err := f.db.OrderCheckpoint(f.ctx, f.config.Scope, orders.NativePointStream)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease, err := f.db.ClaimOrderIngestion(f.ctx, orders.IngestionClaimRequest{Scope: f.config.Scope, Stream: orders.NativePointStream, RefreshKey: key, WorkerID: "native-point-fixture", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatal("claim native point ingestion", err)
	}
	result := orders.SourcePage{Scope: f.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: orders.NativePointStream, FreshDetail: true, RefreshKey: key, ExpectedCheckpointRevision: previous.Revision, Complete: true, StartedAt: read.StartedAt, FinishedAt: read.FinishedAt, WindowFrom: read.StartedAt.Add(-time.Hour), WindowTo: read.FinishedAt, IdempotencyKey: key, FenceToken: lease.Token, NativeReads: []orders.NativeOrderPointRead{read}}
	if read.Point == nil {
		result.Complete = false
		result.Exhausted = true
		result.Failures = []orders.ReadFailure{{PostingNumber: read.Correlation.PostingNumber, Code: read.Code}}
	}
	return result
}

func commitNativePointFixture(t *testing.T, f orderInventoryFixture, page orders.SourcePage) error {
	t.Helper()
	if err := page.Validate(); err != nil {
		return err
	}
	_, err := f.db.CommitOrderPage(f.ctx, page)
	return err
}

func TestNativeOrderPointsPostgresDedupFreshAttemptsAndFailure(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Second))
	first := nativePointPage(t, f, read, "point-first")
	if err := commitNativePointFixture(t, f, first); err != nil {
		t.Fatal(err)
	}
	newer := nativePointReadFixture(t, binding, read.FinishedAt.Add(50*time.Millisecond))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, newer, "point-second")); err != nil {
		t.Fatal(err)
	}
	var snapshots, attempts int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders_native_point_snapshots),(SELECT count(*) FROM orders_native_point_reads)`).Scan(&snapshots, &attempts); err != nil || snapshots != 1 || attempts != 2 {
		t.Fatalf("unchanged facts duplicated snapshots: %d %d %v", snapshots, attempts, err)
	}
	assertNativePointOriginalSnapshot(t, f, read)

	failed := newer
	failed.Point, failed.Code = nil, "NATIVE_READ_FAILED"
	failed.StartedAt, failed.FinishedAt = newer.FinishedAt.Add(time.Millisecond), newer.FinishedAt.Add(2*time.Millisecond)
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, failed, "point-failure")); err != nil {
		t.Fatal(err)
	}
	var code string
	var snapshot *int64
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT code,snapshot_id FROM orders_native_point_reads ORDER BY admitted_at DESC,finished_at DESC,id DESC LIMIT 1`).Scan(&code, &snapshot); err != nil || code != "NATIVE_READ_FAILED" || snapshot != nil {
		t.Fatal("latest failed read falsely reused successful point", err)
	}

	if _, err := f.db.CommitOrderPage(f.ctx, first); err != nil {
		t.Fatal("normal page replay not idempotent", err)
	}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT code FROM orders_native_point_reads ORDER BY admitted_at DESC,finished_at DESC,id DESC LIMIT 1`).Scan(&code); err != nil || code != "NATIVE_READ_FAILED" {
		t.Fatal("old journal replay revived previous point", err)
	}
}

func TestNativeOrderPointsPostgresAtomicScopeAndImmutableFacts(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Second))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, read, "original-point")); err != nil {
		t.Fatal(err)
	}
	changed := nativePointReadFixture(t, binding, read.FinishedAt.Add(time.Millisecond))
	changed.Point.Positions[0].Reserve = orders.Present(orders.Decimal("2"))
	page := nativePointPage(t, f, changed, "conflicting-same-revision")
	if err := commitNativePointFixture(t, f, page); !errors.Is(err, orders.ErrConflict) {
		t.Fatal("same revision conflicting facts accepted", err)
	}
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_ingestion_pages WHERE request_key=$1`, page.IdempotencyKey).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed read left advanced journal/checkpoint", err)
	}
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE orders_native_point_snapshots SET facts_hash=$1`, strings.Repeat("a", 64)); err == nil {
		t.Fatal("immutable snapshot modified")
	}
	if _, err := f.db.Pool.Exec(f.ctx, `DELETE FROM orders_native_point_reads`); err == nil {
		t.Fatal("immutable read history deleted")
	}
	changed.Point.Positions[0].Reserve = read.Point.Positions[0].Reserve
	changed.Correlation.CustomerOrderID = "different-native-order"
	changed.Point.Correlation = changed.Correlation
	page.NativeReads = []orders.NativeOrderPointRead{changed}
	if err := commitNativePointFixture(t, f, page); !errors.Is(err, orders.ErrStale) {
		t.Fatal("binding substitution accepted", err)
	}
}

func TestNativeOrderPointsPostgresScopedKeysetCandidates(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	candidates, err := f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(candidates.Correlations) != 1 || candidates.Correlations[0] != binding {
		t.Fatal("bound terminal order not selected", err)
	}
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Second))
	if err = commitNativePointFixture(t, f, nativePointPage(t, f, read, "fresh-point")); err != nil {
		t.Fatal(err)
	}
	candidates, err = f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(candidates.Correlations) != 0 {
		t.Fatal("fresh no-op point polled unnecessarily", err)
	}
	scope := f.config.Scope
	scope.Tenant = "another-merchant"
	candidates, err = f.db.NativeOrderCandidates(f.ctx, scope, 1, "")
	if err != nil || len(candidates.Correlations) != 0 {
		t.Fatal("point candidates crossed tenant", err)
	}
	candidates, err = f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, binding.PostingNumber)
	if err != nil || len(candidates.Correlations) != 0 {
		t.Fatal("keyset cursor repeated order", err)
	}
}

func TestNativeOrderPointsPostgresKeysetPaginationAndAdmission(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	posting := orderInventoryPosting(t, f, "point-posting-second", "awaiting_packaging")
	second, err := readNativePointBinding(f.ctx, f.db.Pool, f.config.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	second.CustomerOrderID = "second-native-order"
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if err = resolveOrderProjection(f.ctx, tx, second, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	first, err := f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(first.Correlations) != 1 || first.Correlations[0] != binding || first.NextCursor != binding.PostingNumber {
		t.Fatal("first keyset page invalid", err)
	}
	next, err := f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, first.NextCursor)
	if err != nil || len(next.Correlations) != 1 || next.Correlations[0] != second || next.NextCursor != "" {
		t.Fatal("second keyset page skipped or repeated target", err)
	}
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(time.Hour))
	if err = commitNativePointFixture(t, f, nativePointPage(t, f, read, "future-client-clock")); !errors.Is(err, orders.ErrStale) {
		t.Fatal("future client clock admitted as fresh evidence", err)
	}
}

func assertNativePointOriginalSnapshot(t *testing.T, f orderInventoryFixture, read orders.NativeOrderPointRead) {
	t.Helper()
	var original orders.NativeOrderPoint
	var raw []byte
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT point FROM orders_native_point_snapshots`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := ordersDecode(raw, &original); err != nil || !original.StartedAt.Equal(read.StartedAt) || original.Positions[0].Reserve.Presence != orders.PresenceNull {
		t.Fatal("original snapshot or unknown reserve rewritten", err)
	}
}

func TestNativeOrderPointsPostgresAdmissionSurvivesRenewalAndReconciliationRace(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Second))
	page := nativePointPage(t, f, read, "admitted-before-reconciliation")
	var admitted, renewed time.Time
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT attempt_started_at FROM orders_ingestion_leases`).Scan(&admitted); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := lockOrderIngestion(f.ctx, tx, page)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if valid, e := f.db.RenewOrderIngestion(f.ctx, lease, time.Now().Add(time.Minute)); e != nil || !valid {
		t.Fatal("renew native read lease", e)
	}
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT attempt_started_at FROM orders_ingestion_leases`).Scan(&renewed); err != nil || !renewed.Equal(admitted) {
		t.Fatal("renewal invented a later admission", err)
	}
	seedNativePointReconciliation(t, f, read)
	if err = commitNativePointFixture(t, f, page); err != nil {
		t.Fatal(err)
	}
	due, err := f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(due.Correlations) != 1 {
		t.Fatal("late page commit hid post-read reconciliation", err)
	}
	newer := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Millisecond))
	if err = commitNativePointFixture(t, f, nativePointPage(t, f, newer, "admitted-after-reconciliation")); err != nil {
		t.Fatal(err)
	}
	due, err = f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(due.Correlations) != 0 {
		t.Fatal("fresh admitted point remained due", err)
	}
}

func seedNativePointReconciliation(t *testing.T, f orderInventoryFixture, read orders.NativeOrderPointRead) {
	t.Helper()
	now := time.Time{}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	command := orders.Command{ID: "point-native-create", Scope: f.config.Scope, Action: orders.ActionMirrorCreate, PostingNumber: read.Correlation.PostingNumber, Correlation: read.Correlation, CustomerOrder: &orders.CustomerOrderWrite{StoreID: f.config.StoreID}}
	work := orders.Work{ID: "point-native-work", OperationID: "point-native-operation", Scope: f.config.Scope, ConfigRevision: f.config.Revision, Command: command, State: orders.StateReconciled, DispatchCount: 1, CommandFingerprint: strings.Repeat("a", 64)}
	target := orders.TargetObservation{Scope: f.config.Scope, Correlation: read.Correlation, Revision: read.Point.TargetRevision, Complete: true, ObservedAt: now}
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	orderInventorySeedWork(t, f, tx, work, target)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOrderPointsPostgresMigrationRollbackPreservesHistory(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	raw, err := os.ReadFile("../../migrations/00060_orders_native_points.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("migration missing rollback")
	}
	if _, err = f.db.Pool.Exec(f.ctx, parts[1]); err != nil {
		t.Fatal("empty rollback failed", err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, parts[0]); err != nil {
		t.Fatal("upgrade after rollback failed", err)
	}
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Second))
	if err = commitNativePointFixture(t, f, nativePointPage(t, f, read, "point-history")); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded native read history")
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_native_point_reads`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed rollback damaged history", err)
	}
}

func nativePointTestURL(explicit, fallback string) (*url.URL, error) {
	raw, explicitSet := explicit, explicit != ""
	if !explicitSet {
		raw = fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("native point tests require explicit dedicated localhost PostgreSQL test DSN")
	}
	if explicitSet && u.Path != "/styk_orders_point_test" || !explicitSet && u.Path != "/styk_orders_release_test" && u.Path != "/styk_orders_store_test" {
		return nil, errors.New("native point tests require styk_orders_point_test or an allowlisted orders test DSN")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("native point test DSN query is invalid")
	}
	for key, values := range query {
		if key != "sslmode" || len(values) != 1 {
			return nil, errors.New("native point test DSN overrides are forbidden")
		}
	}
	u.Path, u.RawPath = "/styk_orders_point_test", ""
	return u, nil
}

func TestNativeOrderPointTestDatabaseScopeGuard(t *testing.T) {
	for _, tc := range []struct {
		name, explicit, fallback string
		valid                    bool
	}{
		{"named override", "postgres://localhost/styk_orders_point_test?sslmode=disable", "", true},
		{"derived fixture", "", "postgres://127.0.0.1/styk_orders_release_test?sslmode=disable", true},
		{"no implicit database", "", "", false},
		{"protected database", "", "postgres://localhost/styk", false},
		{"remote server", "postgres://remote.example/styk_orders_point_test", "", false},
		{"database query override", "", "postgres://localhost/styk_orders_release_test?dbname=styk", false},
		{"search path override", "postgres://localhost/styk_orders_point_test?options=-csearch_path=public", "", false},
		{"malformed query override", "postgres://localhost/styk_orders_point_test?sslmode=disable;options=unsafe", "", false},
		{"host query override", "", "postgres://localhost/styk_orders_release_test?host=remote.example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := nativePointTestURL(tc.explicit, tc.fallback)
			if (err == nil) != tc.valid {
				t.Fatal("unexpected test DB guard decision")
			}
			if err == nil && u.Path != "/styk_orders_point_test" {
				t.Fatal("point test connected to another database")
			}
		})
	}
}

func TestNativeOrderPointsPostgresLaterFailedClaimWinsAcrossClientClockSkew(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	ahead := nativePointReadFixture(t, binding, time.Now().UTC().Add(2*time.Second))
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, ahead, "ahead-clock-success")); err != nil {
		t.Fatal(err)
	}
	reconciled := nativePointReadFixture(t, binding, time.Now().UTC())
	reconciled.Point.TargetRevision = "later-native-state"
	seedNativePointReconciliation(t, f, reconciled)
	behind := orders.NativeOrderPointRead{Correlation: binding, CredentialRevision: 1, StartedAt: time.Now().UTC().Add(-2 * time.Second), FinishedAt: time.Now().UTC().Add(-time.Second), Code: "NATIVE_READ_FAILED"}
	if err := commitNativePointFixture(t, f, nativePointPage(t, f, behind, "later-behind-clock-failure")); err != nil {
		t.Fatal(err)
	}
	candidates, err := f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(candidates.Correlations) != 0 {
		t.Fatal("older successful client clock overrode later failed DB-admitted read", err)
	}
	var code string
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT code FROM orders_native_point_reads ORDER BY admitted_at DESC,finished_at DESC,id DESC LIMIT 1`).Scan(&code); err != nil || code != "NATIVE_READ_FAILED" {
		t.Fatal("later failure did not invalidate previous successful point", err)
	}
}

func TestNativeOrderPointsPostgresOldReadClaimCannotRevivePoint(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	old := nativePointReadFixture(t, binding, time.Now().UTC().Add(-time.Second))
	stalePage := nativePointPage(t, f, old, "interrupted-original-read")
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := lockOrderIngestion(f.ctx, tx, stalePage)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.db.ReleaseOrderIngestion(f.ctx, lease); err != nil {
		t.Fatal(err)
	}
	failed := orders.NativeOrderPointRead{Correlation: binding, CredentialRevision: 1, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Code: "NATIVE_READ_FAILED"}
	if err = commitNativePointFixture(t, f, nativePointPage(t, f, failed, "new-owner-read-failed")); err != nil {
		t.Fatal(err)
	}
	if err = commitNativePointFixture(t, f, stalePage); !errors.Is(err, orders.ErrStale) && !errors.Is(err, orders.ErrNotFound) {
		t.Fatal("old network read accepted under stale claim", err)
	}
	var snapshots int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_native_point_snapshots`).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatal("stale response revived successful snapshot", err)
	}
}

func TestNativeOrderPointsPostgresPeriodicDueUsesDBClockNotClientFuture(t *testing.T) {
	f, binding := nativePointStoreFixture(t)
	read := nativePointReadFixture(t, binding, time.Now().UTC().Add(2*time.Second))
	page := nativePointPage(t, f, read, "periodic-clock-skew")
	// Synthetic setup models a long-running, renewed lease. No stored read or
	// historical operation is rewritten; provider clocks remain untouched.
	inventoryExec(t, f.db, `UPDATE orders_ingestion_leases SET attempt_started_at=clock_timestamp()-interval '301 seconds' WHERE token=$1`, page.FenceToken)
	if err := commitNativePointFixture(t, f, page); err != nil {
		t.Fatal(err)
	}
	due, err := f.db.NativeOrderCandidates(f.ctx, f.config.Scope, 1, "")
	if err != nil || len(due.Correlations) != 1 || due.Correlations[0] != binding {
		t.Fatal("future client finish time stalled periodic DB scheduling", err)
	}
}
