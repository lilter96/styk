//go:build integration

package integration

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

func deliveryFixture(t *testing.T) (*postgres.Store, delivery.Profile) {
	t.Helper()
	db := catalogSyncDB(t)
	scope := delivery.Scope{Tenant: "delivery-tenant", OzonAccount: "delivery-account"}
	if err := db.RegisterStockConnection(context.Background(), scope.Tenant, "ozon", scope.OzonAccount, "delivery isolated test", true); err != nil {
		t.Fatal(err)
	}
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := delivery.Evidence(manifest)
	if err != nil {
		t.Fatal(err)
	}
	warehouse := delivery.Warehouse{ID: "9007199254740993", Type: orders.Present("rfbs_express"), Status: orders.Present("created"), IsRFBS: orders.Present(true), Express: orders.Present(true)}
	method := delivery.Method{ID: "9007199254740995", WarehouseID: warehouse.ID, Express: orders.Present(true)}
	profile, err := delivery.Classify(scope, warehouse, &method, evidence, time.Now().UTC().Add(-10*time.Second).Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	return db, profile
}

func TestDeliveryObservationScopeRefreshAndConcurrency(t *testing.T) {
	db, profile := deliveryFixture(t)
	ctx := context.Background()
	if err := db.SaveDeliveryObservations(ctx, profile.Scope, []delivery.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	refresh := profile
	refresh.ObservedAt, refresh.CheckedAt = time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond), time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	refresh.ValidUntil = refresh.CheckedAt.Add(delivery.Validity)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := db.SaveDeliveryObservations(ctx, profile.Scope, []delivery.Profile{refresh}); err != nil {
				t.Errorf("concurrent refresh: %v", err)
			}
		})
	}
	group.Wait()
	observed, err := db.DeliveryObservations(ctx, profile.Scope)
	if err != nil || len(observed) != 1 || observed[0].Revision != profile.Revision || !observed[0].ObservedAt.Equal(profile.ObservedAt) || !observed[0].CheckedAt.Equal(refresh.CheckedAt) {
		t.Fatalf("stable scoped observation lost on refresh: %v", err)
	}
	other := profile.Scope
	other.Tenant = "other-tenant"
	if observed, err = db.DeliveryObservations(ctx, other); err != nil || len(observed) != 0 {
		t.Fatal("cross-tenant delivery facts leaked")
	}
	other = profile.Scope
	other.OzonAccount = "other-account"
	if observed, err = db.DeliveryObservations(ctx, other); err != nil || len(observed) != 0 {
		t.Fatal("cross-account delivery facts leaked")
	}
}

func TestDeliveryObservationRejectsStaleContractAndRollsBack(t *testing.T) {
	db, profile := deliveryFixture(t)
	ctx := context.Background()
	invalid := profile
	invalid.Contract.Hash = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := db.SaveDeliveryObservations(ctx, profile.Scope, []delivery.Profile{profile, invalid}); !errors.Is(err, orders.ErrStale) {
		t.Fatal("stale contract accepted")
	}
	observed, err := db.DeliveryObservations(ctx, profile.Scope)
	if err != nil || len(observed) != 0 {
		t.Fatal("failed batch persisted a partial observation")
	}
	if err = db.SaveDeliveryObservations(ctx, profile.Scope, []delivery.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE delivery_capability_observations SET contract_hash=repeat('0',64),profile=jsonb_set(profile,'{contract,hash}',to_jsonb(repeat('0',64)))`); err != nil {
		t.Fatal(err)
	}
	if observed, err = db.DeliveryObservations(ctx, profile.Scope); err != nil || len(observed) != 0 {
		t.Fatal("previous contract observations were reused")
	}
}

func TestDeliveryObservationExpiryAndDatabaseOwnership(t *testing.T) {
	db, profile := deliveryFixture(t)
	ctx := context.Background()
	if err := db.SaveDeliveryObservations(ctx, profile.Scope, []delivery.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE delivery_capability_observations SET observed_at=observed_at-interval '10 minutes',checked_at=checked_at-interval '10 minutes',valid_until=valid_until-interval '10 minutes',profile=profile||jsonb_build_object('observed_at',observed_at-interval '10 minutes','checked_at',checked_at-interval '10 minutes','valid_until',valid_until-interval '10 minutes')`); err != nil {
		t.Fatal(err)
	}
	if observed, err := db.DeliveryObservations(ctx, profile.Scope); err != nil || len(observed) != 0 {
		t.Fatal("expired evidence was reused")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE delivery_capability_observations SET tenant_id='other-tenant'`); err == nil {
		t.Fatal("database allowed capability ownership to diverge from profile")
	}
}

func TestDeliveryObservationRollbackPreservesHistory(t *testing.T) {
	db, profile := deliveryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.SaveDeliveryObservations(ctx, profile.Scope, []delivery.Profile{profile}); err != nil {
		t.Fatal(err)
	}
	dsn := db.Pool.Config().ConnString()
	command := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "down-to", "46")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "refusing to discard delivery capability observation history") {
		t.Fatal("rollback did not retain delivery history")
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM delivery_capability_observations`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected rollback changed delivery history")
	}
	if _, err = db.Pool.Exec(ctx, `DELETE FROM delivery_capability_observations`); err != nil {
		t.Fatal(err)
	}
	catalogSyncMigrate(t, dsn, "down-to", "46")
}
