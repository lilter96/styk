//go:build integration

package integration

import (
	"context"
	"example.com/styk/internal/domain"
	"example.com/styk/internal/postgres"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestWarehouseMapping(t *testing.T) {
	ctx := context.Background()
	db, e := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := postgres.MatchScope{Tenant: "t" + suffix, MoyskladAccount: "m" + suffix, OzonAccount: "o" + suffix}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM warehouse_mapping_history WHERE tenant_id=$1`, `DELETE FROM warehouse_mappings WHERE tenant_id=$1`} {
			if _, e := db.Pool.Exec(ctx, q, scope.Tenant); e != nil {
				t.Error(e)
			}
		}
		if _, e := db.Pool.Exec(ctx, `DELETE FROM source_imports WHERE account_id=$1 OR account_id=$2`, scope.MoyskladAccount, scope.OzonAccount); e != nil {
			t.Error(e)
		}
	})
	now := time.Now()
	f := false
	ms := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "store", ID: "m1", Name: "same"}, {Kind: "store", ID: "m2", Name: "same"}}}
	oz := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Warehouses: []domain.OzonWarehouse{{ID: "o1", Name: "same", IsRFBS: &f}, {ID: "o2", Name: "same", IsRFBS: &f}}}
	if _, e = db.SaveInventoryImport(ctx, ms, "m"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SaveOzonImport(ctx, oz, "o"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ConfirmWarehouseMapping(ctx, scope, "missing", "o1", "operator"); e == nil {
		t.Fatal("missing warehouse accepted")
	}
	var wg sync.WaitGroup
	ch := make(chan error, 2)
	for _, d := range []string{"o1", "o2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.ConfirmWarehouseMapping(ctx, scope, "m1", d, "operator")
			ch <- err
		}()
	}
	wg.Wait()
	close(ch)
	success := 0
	for err := range ch {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("success count %d", success)
	}
	var target string
	if e = db.Pool.QueryRow(ctx, `SELECT ozon_warehouse_id FROM warehouse_mappings WHERE tenant_id=$1`, scope.Tenant).Scan(&target); e != nil {
		t.Fatal(e)
	}
	created, e := db.ConfirmWarehouseMapping(ctx, scope, "m1", target, "operator")
	if e != nil || created {
		t.Fatalf("repeat %v %v", created, e)
	}
	if _, e = db.ConfirmWarehouseMapping(ctx, scope, "m2", target, "operator"); e == nil {
		t.Fatal("target reused")
	}
	ms.Entities = nil
	if _, e = db.SaveInventoryImport(ctx, ms, "m2"); e != nil {
		t.Fatal(e)
	}
	created, e = db.ConfirmWarehouseMapping(ctx, scope, "m1", target, "operator")
	if e != nil || created {
		t.Fatal("historical mapping lost")
	}
	other := scope
	other.MoyskladAccount = "wrong-account"
	if _, err := db.ConfirmWarehouseMapping(ctx, other, "m1", target, "operator"); err == nil {
		t.Fatal("wrong account accepted")
	}
	other = scope
	other.Tenant += "-different"
	// An unrelated tenant has no visibility through the scoped query.
	var otherCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1`, other.Tenant).Scan(&otherCount); err != nil || otherCount != 0 {
		t.Fatal("cross-tenant mapping leak")
	}
	var count int
	if e = db.Pool.QueryRow(ctx, `SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id=$1`, scope.Tenant).Scan(&count); e != nil || count != 1 {
		t.Fatalf("history %d %v", count, e)
	}
}

func TestWarehouseMappingAuditRollback(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := postgres.MatchScope{Tenant: "rollback-" + suffix, MoyskladAccount: "m-" + suffix, OzonAccount: "o-" + suffix}
	now := time.Now()
	f := false
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `ALTER TABLE warehouse_mapping_history DROP CONSTRAINT IF EXISTS test_warehouse_audit_failure`)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM warehouse_mapping_history WHERE tenant_id=$1`, scope.Tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM warehouse_mappings WHERE tenant_id=$1`, scope.Tenant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM source_imports WHERE account_id IN ($1,$2)`, scope.MoyskladAccount, scope.OzonAccount)
	})
	if _, err = db.SaveInventoryImport(ctx, domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "store", ID: "m"}}}, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveOzonImport(ctx, domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Warehouses: []domain.OzonWarehouse{{ID: "o", IsRFBS: &f}}}, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE warehouse_mapping_history ADD CONSTRAINT test_warehouse_audit_failure CHECK (provenance <> 'audit-failure')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmWarehouseMapping(ctx, scope, "m", "o", "audit-failure"); err == nil {
		t.Fatal("expected audit insert failure")
	}
	var mappings, history int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1),(SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id=$1)`, scope.Tenant).Scan(&mappings, &history); err != nil || mappings != 0 || history != 0 {
		t.Fatalf("rollback: mappings=%d history=%d error=%v", mappings, history, err)
	}
}
