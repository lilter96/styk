//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"example.com/styk/internal/domain"
	"example.com/styk/internal/matching"
	"example.com/styk/internal/postgres"
)

func TestProductMatching(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := postgres.MatchScope{Tenant: "tenant-" + suffix, OzonAccount: "ozon-" + suffix, MoyskladAccount: "moy-" + suffix}
	t.Cleanup(func() {
		_, e := db.Pool.Exec(ctx, `DELETE FROM product_mapping_history WHERE tenant_id=$1`, scope.Tenant)
		if e != nil {
			t.Error(e)
		}
		_, e = db.Pool.Exec(ctx, `DELETE FROM product_mappings WHERE tenant_id=$1`, scope.Tenant)
		if e != nil {
			t.Error(e)
		}
		_, e = db.Pool.Exec(ctx, `DELETE FROM source_imports WHERE account_id IN ($1,$2)`, scope.OzonAccount, scope.MoyskladAccount)
		if e != nil {
			t.Error(e)
		}
	})
	now := time.Now()
	osnap := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1", OfferID: "SHARED"}, {ProductID: "2", OfferID: "SECOND"}, {ProductID: "4", OfferID: " SECOND "}}}
	msnap := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "p1", Article: "SHARED", Code: "SECOND"}, {Kind: "variant", ID: "v1", Article: "SHARED"}}}
	if _, err = db.SaveOzonImport(ctx, osnap, "o1"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveInventoryImport(ctx, msnap, "m1"); err != nil {
		t.Fatal(err)
	}
	data, err := db.MatchData(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	r := matching.New(data.Targets, data.Mappings).Match(scope.Tenant, data.Sources[0], scope.MoyskladAccount)
	if r.Status != matching.MultipleCandidates || len(r.Candidates) != 2 {
		t.Fatalf("candidates %+v", r)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, target := range []struct{ kind, id string }{{"product", "p1"}, {"variant", "v1"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := db.ConfirmMatch(ctx, scope, "1", target.kind, target.id, "test operator")
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent confirmations succeeded %d times", success)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mapping_history WHERE tenant_id=$1`, scope.Tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history %d %v", count, err)
	}
	// A second offer can map to p1; it must be reported as shared when p1 was selected above.
	_, err = db.ConfirmMatch(ctx, scope, "2", "product", "p1", "test operator")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ConfirmMatch(ctx, scope, "4", "product", "p1", "test operator")
	if err != nil {
		t.Fatal(err)
	}
	data, err = db.MatchData(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	r = matching.New(data.Targets, data.Mappings).Match(scope.Tenant, data.Sources[1], scope.MoyskladAccount)
	if !r.SharedInventory {
		t.Fatal("shared inventory not reported")
	}
	var firstTarget string
	err = db.Pool.QueryRow(ctx, `SELECT target_id FROM product_mappings WHERE tenant_id=$1 AND ozon_product_id='1'`, scope.Tenant).Scan(&firstTarget)
	if err != nil {
		t.Fatal(err)
	}
	if firstTarget == "p1" && !r.SharedInventory {
		t.Fatal("shared inventory not reported")
	}
	// Different tenant does not see these confirmations.
	other := scope
	other.Tenant += "-other"
	otherData, err := db.MatchData(ctx, other)
	if err != nil || len(otherData.Mappings) != 0 {
		t.Fatalf("tenant leak %v", err)
	}
	// Removed source rows are not interpreted as a deletion of confirmed mappings.
	msnap.Entities = nil
	if _, err = db.SaveInventoryImport(ctx, msnap, "m2"); err != nil {
		t.Fatal(err)
	}
	data, err = db.MatchData(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	r = matching.New(data.Targets, data.Mappings).Match(scope.Tenant, data.Sources[0], scope.MoyskladAccount)
	if r.Confirmed == nil || r.Status != matching.AlreadyConfirmed {
		t.Fatalf("mapping disappeared %+v", r)
	}
	// A uniqueness failure must not append history or create a partial mapping.
	osnap.Products = append(osnap.Products, domain.OzonProduct{ProductID: "3", OfferID: "THIRD"})
	if _, err = db.SaveOzonImport(ctx, osnap, "o2"); err != nil {
		t.Fatal(err)
	}
	msnap.Entities = []domain.ExternalEntity{{Kind: "product", ID: "p3", Article: "THIRD"}}
	if _, err = db.SaveInventoryImport(ctx, msnap, "m3"); err != nil {
		t.Fatal(err)
	}
	// Fail the audit insert after the current mapping is inserted, then verify rollback.
	_, err = db.Pool.Exec(ctx, `ALTER TABLE product_mapping_history ADD CONSTRAINT matching_test_audit_failure CHECK (provenance <> 'simulate-audit-failure')`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := db.Pool.Exec(context.Background(), `ALTER TABLE product_mapping_history DROP CONSTRAINT IF EXISTS matching_test_audit_failure`); e != nil {
			t.Error(e)
		}
	})
	if _, err = db.ConfirmMatch(ctx, scope, "3", "product", "p3", "simulate-audit-failure"); err == nil {
		t.Fatal("audit failure accepted")
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings WHERE tenant_id=$1 AND ozon_product_id='3'`, scope.Tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mapping not rolled back %d %v", count, err)
	}
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE product_mapping_history DROP CONSTRAINT matching_test_audit_failure`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,'unrelated','THIRD',$3,'product','p3',1,'fixture')`, scope.Tenant, scope.OzonAccount, scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmMatch(ctx, scope, "3", "product", "p3", "test operator"); err == nil {
		t.Fatal("conflicting offer accepted")
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings WHERE tenant_id=$1 AND ozon_product_id='3'`, scope.Tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial mapping %d %v", count, err)
	}
}
