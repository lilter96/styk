//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

func TestOrdersProcessDeliveryObservationCanArriveOutOfOrder(t *testing.T) {
	db, _ := ordersProcessDB(t)
	const tenant, account = "delivery-process-tenant", "delivery-process-account"
	ordersStoreOwner(t, db, tenant)
	if err := db.RegisterStockConnection(context.Background(), tenant, "ozon", account, "Synthetic delivery fixture", true); err != nil {
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
	scope := delivery.Scope{Tenant: tenant, OzonAccount: account}
	warehouse := delivery.Warehouse{ID: "3001", Type: orders.Present("fbs"), Status: orders.Present("created"), Express: orders.Present(false), IsRFBS: orders.Present(false)}
	checked := time.Now().Truncate(time.Microsecond).Add(-time.Second + 999*time.Nanosecond)
	newer, err := delivery.Classify(scope, warehouse, nil, evidence, checked)
	if err != nil {
		t.Fatal(err)
	}
	newer.ObservedAt = newer.ObservedAt.Add(999 * time.Nanosecond)
	newer.CheckedAt = newer.CheckedAt.Add(999 * time.Nanosecond)
	newer.ValidUntil = newer.ValidUntil.Add(999 * time.Nanosecond)
	older := newer
	older.CheckedAt, older.ObservedAt, older.ValidUntil = newer.CheckedAt.Add(-time.Second), newer.ObservedAt.Add(-time.Second), newer.ValidUntil.Add(-time.Second)
	ordersProcessSaveDelivery(t, db, scope, newer)
	ordersProcessSaveDelivery(t, db, scope, older)
	var storedChecked, valid time.Time
	err = db.Pool.QueryRow(context.Background(), `SELECT checked_at,valid_until FROM delivery_capability_observations WHERE tenant_id=$1 AND ozon_account=$2`, tenant, account).Scan(&storedChecked, &valid)
	if err != nil || storedChecked.Before(newer.CheckedAt.Add(-time.Microsecond)) || valid.Before(newer.ValidUntil.Add(-time.Microsecond)) {
		t.Fatal("late identical process observation rewound delivery freshness")
	}
}

func ordersProcessSaveDelivery(t *testing.T, db *postgres.Store, scope delivery.Scope, profile delivery.Profile) {
	t.Helper()
	if err := db.SaveDeliveryObservations(context.Background(), scope, []delivery.Profile{profile}); err != nil {
		t.Fatal("identical concurrent process delivery observation must succeed")
	}
}
