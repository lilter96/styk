//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func postingStore(t *testing.T) (*postgres.Store, string) {
	t.Helper()
	db, err := postgres.Open(context.Background(), os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	tenant := fmt.Sprintf("postings-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, e := db.Pool.Exec(context.Background(), `DELETE FROM ozon_posting_batches WHERE tenant_id=$1`, tenant); e != nil {
			t.Error(e)
		}
		if _, e := db.Pool.Exec(context.Background(), `DELETE FROM ozon_posting_identities WHERE tenant_id=$1`, tenant); e != nil {
			t.Error(e)
		}
	})
	return db, tenant
}
func postingBatch(tenant, account string, at time.Time, records ...domain.PostingObservation) domain.PostingImport {
	return domain.PostingImport{Tenant: tenant, Account: account, Since: at.Add(-time.Hour), Until: at.Add(time.Hour), StartedAt: at, FinishedAt: at.Add(time.Second), Postings: records}
}
func postingFixture(number, status string, at time.Time, lines ...domain.PostingLine) domain.PostingObservation {
	return domain.PostingObservation{PostingNumber: number, OrderID: "order-id", OrderNumber: "order-number", Status: status, InProcessAt: "2026-01-01T08:00:00Z", ObservedAt: at, ObservedTimeBasis: "detail_received", Lines: lines}
}
func TestPostingHistoryCancellationCompositionAndOrdering(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	lineA := domain.PostingLine{OfferID: "item-a", SKU: "11", Quantity: 3}
	lineB := domain.PostingLine{OfferID: "item-b", SKU: "12", Quantity: 1}
	reason := "unknown_reason"
	reasonID := int64(987)
	after := true
	// The last observation arrives later but carries an earlier local measurement time;
	// without a source update time there is no claim about source-state ordering.
	fixtures := []struct {
		name  string
		posts []domain.PostingObservation
	}{
		{"two postings on one order", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at, lineA, lineB), postingFixture("p2", "awaiting_packaging", at, lineB)}},
		{"quantity changed without status", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at.Add(time.Second), domain.PostingLine{OfferID: "item-a", SKU: "11", Quantity: 1}, lineB)}},
		{"product removed", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at.Add(2*time.Second), lineB)}},
		{"product added", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at.Add(3*time.Second), lineB, lineA)}},
		{"unchanged composition", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at.Add(4*time.Second), lineB, lineA)}},
		{"cancel only p1", []domain.PostingObservation{postingFixture("p1", "cancelled", at.Add(5*time.Second), lineB)}},
		{"repeat cancellation", []domain.PostingObservation{postingFixture("p1", "cancelled", at.Add(6*time.Second), lineB)}},
		{"late older measurement", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at.Add(-time.Minute), lineA)}},
		{"related products kept as separate lines", []domain.PostingObservation{postingFixture("p1", "awaiting_packaging", at.Add(7*time.Second), domain.PostingLine{OfferID: "related", SKU: "101", Quantity: 1}, domain.PostingLine{OfferID: "related", SKU: "102", Quantity: 2})}},
	}
	fixtures[5].posts[0].ParentPostingNumber = "parent"
	fixtures[5].posts[0].FactDeliveryDate = "2026-01-05T00:00:00Z"
	fixtures[5].posts[0].Cancellation = &domain.PostingCancellation{Reason: &reason, ReasonID: &reasonID, CancelledAfterShip: &after}
	fixtures[6].posts[0].Cancellation = &domain.PostingCancellation{}
	for i, tc := range fixtures {
		b := postingBatch(tenant, "seller", at.Add(time.Duration(i)*time.Minute), tc.posts...)
		if _, err := db.SavePostings(ctx, b); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	results, err := db.ListPostings(ctx, domain.PostingReadScope{Tenant: tenant, Account: "seller", MoyskladAccount: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("observations %d", len(results))
	}
	if results[0].Observation.OrderID != results[1].Observation.OrderID || results[0].Observation.PostingNumber == results[1].Observation.PostingNumber || results[1].Observation.Status != "awaiting_packaging" {
		t.Fatal("postings collapsed or sibling cancellation inferred")
	}
	if results[0].Lines[0].Quantity != 3 || results[2].Lines[0].Quantity != 1 || results[0].Observation.Status != results[2].Observation.Status {
		t.Fatal("quantity change was lost")
	}
	if results[3].Observation.Status != results[4].Observation.Status || len(results[3].Lines) != 1 || results[3].Lines[0].OfferID != "item-b" || len(results[4].Lines) != 2 || results[4].Lines[1].OfferID != "item-a" {
		t.Fatal("composition changes lost")
	}
	if !reflect.DeepEqual(results[4].Lines, results[5].Lines) {
		t.Fatal("identical composition changed")
	}
	cancelled := results[6].Observation
	if cancelled.Status != "cancelled" || cancelled.ParentPostingNumber != "parent" || cancelled.FactDeliveryDate != "2026-01-05T00:00:00Z" || cancelled.Cancellation == nil || *cancelled.Cancellation.Reason != reason || *cancelled.Cancellation.ReasonID != reasonID || !*cancelled.Cancellation.CancelledAfterShip {
		t.Fatalf("lost cancellation %+v", cancelled)
	}
	repeat := results[7].Observation
	if repeat.Status != "cancelled" || repeat.Cancellation == nil || repeat.Cancellation.Reason != nil || repeat.Cancellation.CancelledAfterShip != nil {
		t.Fatal("missing optional details treated as values")
	}
	last := results[8].Observation
	if last.Status != "awaiting_packaging" || !last.ObservedAt.Before(cancelled.ObservedAt) || last.SourceCreatedAt != nil || last.SourceUpdatedAt != nil || last.ObservedTimeBasis != "detail_received" {
		t.Fatal("out of order observation misrepresented")
	}
	if len(results[9].Lines) != 2 || results[9].Lines[0].SKU != "101" || results[9].Lines[1].SKU != "102" || results[9].Lines[0].OfferID != results[9].Lines[1].OfferID {
		t.Fatal("related product lines collapsed")
	}
	if results[0].Observation.Cancellation != nil {
		t.Fatal("missing cancellation became a cancellation")
	}
}

func TestPostingCorrelationScopeAndCurrentMappings(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	accountA, accountB, accountC := "seller-A-"+tenant, "seller-B-"+tenant, "seller-C-"+tenant
	tenantB := tenant + "-other"
	moy := "moy-" + tenant
	moyB := "moy-other-" + tenant
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM product_mapping_history WHERE tenant_id IN ($1,$2)`, `DELETE FROM warehouse_mapping_history WHERE tenant_id IN ($1,$2)`, `DELETE FROM product_mappings WHERE tenant_id IN ($1,$2)`, `DELETE FROM warehouse_mappings WHERE tenant_id IN ($1,$2)`, `DELETE FROM ozon_posting_batches WHERE tenant_id IN ($1,$2)`, `DELETE FROM ozon_posting_identities WHERE tenant_id IN ($1,$2)`} {
			if _, err := db.Pool.Exec(ctx, q, tenant, tenantB); err != nil {
				t.Error(err)
			}
		}
		if _, err := db.Pool.Exec(ctx, `DELETE FROM source_imports WHERE account_id IN ($1,$2,$3,$4,$5)`, accountA, accountB, accountC, moy, moyB); err != nil {
			t.Error(err)
		}
	})
	for _, c := range []struct{ tenant, provider, account string }{{tenant, "ozon", accountA}, {tenant, "ozon", accountB}, {tenant, "moysklad", moy}, {tenantB, "ozon", accountC}, {tenantB, "moysklad", moyB}} {
		if err := db.RegisterStockConnection(ctx, c.tenant, c.provider, c.account, "synthetic fixture", true); err != nil {
			t.Fatal(err)
		}
	}
	for _, account := range []string{accountA, accountB, accountC} {
		b := domain.OzonImport{Account: account, StartedAt: at, FinishedAt: at, Products: []domain.OzonProduct{{ProductID: "1", OfferID: "matched", SKU: "11"}, {ProductID: "2", OfferID: "conflicting", SKU: "22"}, {ProductID: "3", OfferID: "suggestion", SKU: "33"}, {ProductID: "4", OfferID: "other-scope", SKU: "44"}, {ProductID: "5", OfferID: "no-sku", SKU: ""}}}
		if _, err := db.SaveOzonImport(ctx, b, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	ms := domain.InventoryImport{Account: moy, StartedAt: at, FinishedAt: at, Entities: []domain.ExternalEntity{{Kind: "product", ID: "unconfirmed", Article: "suggestion"}}}
	if _, err := db.SaveInventoryImport(ctx, ms, "fixture"); err != nil {
		t.Fatal(err)
	}
	ms.Account = moyB
	if _, err := db.SaveInventoryImport(ctx, ms, "fixture"); err != nil {
		t.Fatal(err)
	}
	insertProduct := func(tenantID, account, product, offer, target string) {
		t.Helper()
		moyAccount := moy
		if tenantID == tenantB {
			moyAccount = moyB
		}
		_, err := db.Pool.Exec(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,$3,$4,$5,'product',$6,1,'fixture')`, tenantID, account, product, offer, moyAccount, target)
		if err != nil {
			t.Fatal(err)
		}
	}
	insertProduct(tenant, accountA, "1", "matched", "mapped-1")
	insertProduct(tenant, accountA, "2", "conflicting", "mapped-2")
	insertProduct(tenant, accountA, "5", "no-sku", "mapped-5")
	insertProduct(tenantB, accountC, "4", "other-scope", "other-tenant")
	insertProduct(tenant, accountB, "4", "other-scope", "other-account")
	for _, spec := range []struct{ t, a, m, source string }{{tenant, accountA, moy, "warehouse-A"}, {tenantB, accountC, moyB, "warehouse-other-tenant"}, {tenant, accountB, moy, "warehouse-other-account"}} {
		_, err := db.Pool.Exec(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance) VALUES($1,$2,$3,$4,'w1','active',1,'fixture')`, spec.t, spec.m, spec.a, spec.source)
		if err != nil {
			t.Fatal(err)
		}
	}
	lines := []domain.PostingLine{{OfferID: "matched", SKU: "11", Quantity: 1}, {OfferID: "suggestion", SKU: "33", Quantity: 2}, {OfferID: "conflicting", SKU: "99", Quantity: 3}, {OfferID: "other-scope", SKU: "44", Quantity: 1}, {OfferID: "no-sku", SKU: "55", Quantity: 1}, {OfferID: "", SKU: "404", Quantity: 1}}
	p := postingFixture("same-posting", "active", at, lines...)
	p.WarehouseID = "w1"
	for _, scope := range []struct{ t, a string }{{tenant, accountA}, {tenant, accountB}, {tenantB, accountC}} {
		if _, err := db.SavePostings(ctx, postingBatch(scope.t, scope.a, at, p)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(tid, account, m string) []domain.PostingView {
		t.Helper()
		rows, err := db.ListPostings(ctx, domain.PostingReadScope{Tenant: tid, Account: account, MoyskladAccount: m})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := read(tenant, accountA, moy)
	if len(rows) != 1 || rows[0].Tenant != tenant || rows[0].Account != accountA || len(rows[0].Lines) != 6 {
		t.Fatalf("scope rows %+v", rows)
	}
	r := rows[0]
	if r.Warehouse.Status != "confirmed" || r.Warehouse.WarehouseID == nil || *r.Warehouse.WarehouseID != "warehouse-A" {
		t.Fatalf("warehouse correlation %+v", r.Warehouse)
	}
	statuses := []string{"confirmed", "unmatched", "conflict", "unmatched", "unverified", "unmatched"}
	for i, want := range statuses {
		if r.Lines[i].Correlation.Status != want {
			t.Errorf("line %d got %+v want %s", i, r.Lines[i].Correlation, want)
		}
	}
	if r.Lines[0].Correlation.TargetID == nil || *r.Lines[0].Correlation.TargetID != "mapped-1" {
		t.Fatal("confirmed product not correlated")
	}
	if r.Lines[2].Correlation.TargetID != nil || r.Lines[3].Correlation.TargetID != nil {
		t.Fatal("conflicting or cross-scope mapping leaked")
	}
	b := read(tenant, accountB, moy)
	if len(b) != 1 || b[0].Warehouse.WarehouseID == nil || *b[0].Warehouse.WarehouseID != "warehouse-other-account" || b[0].Lines[0].Correlation.Status != "unmatched" || b[0].Lines[3].Correlation.Status != "confirmed" {
		t.Fatalf("account separation %+v", b)
	}
	other := read(tenantB, accountC, moyB)
	if len(other) != 1 || other[0].Warehouse.WarehouseID == nil || *other[0].Warehouse.WarehouseID != "warehouse-other-tenant" || other[0].Lines[0].Correlation.Status != "unmatched" {
		t.Fatalf("tenant separation %+v", other)
	}
	// Distinct identities in another scope cannot be attached to this batch.
	for i, bad := range []struct{ tenant, account string }{{tenantB, accountC}, {tenant, accountB}} {
		number := fmt.Sprintf("foreign-posting-%d", i)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO ozon_posting_identities(tenant_id,account_id,posting_number,order_id) VALUES($1,$2,$3,'order-id')`, bad.tenant, bad.account, number); err != nil {
			t.Fatal(err)
		}
		_, err := db.Pool.Exec(ctx, `INSERT INTO ozon_posting_observations(batch_id,tenant_id,account_id,posting_number,order_id,order_number,status,substatus,warehouse_id,in_process_at,shipment_date,delivering_date,observed_at) VALUES($1,$2,$3,$4,'order-id','order-number','active','','','','','',$5)`, r.BatchID, bad.tenant, bad.account, number, at)
		var dbErr *pgconn.PgError
		if !errors.As(err, &dbErr) || dbErr.ConstraintName != "posting_observation_batch_scope" {
			t.Fatalf("cross-scope insert was not rejected by batch scope FK: %v", err)
		}
	}
	_, identityInsertErr := db.Pool.Exec(ctx, `INSERT INTO ozon_posting_observations(batch_id,tenant_id,account_id,posting_number,order_id,order_number,status,substatus,warehouse_id,in_process_at,shipment_date,delivering_date,observed_at) VALUES($1,$2,$3,'unregistered','order-id','order-number','active','','','','','',$4)`, r.BatchID, tenant, accountA, at)
	var identityErr *pgconn.PgError
	if !errors.As(identityInsertErr, &identityErr) || identityErr.ConstraintName != "posting_observation_identity" {
		t.Fatalf("missing posting identity accepted: %v", identityInsertErr)
	}
	if len(read(tenant+"-absent", accountA, moy)) != 0 || len(read(tenant, accountA+"-absent", moy)) != 0 {
		t.Fatal("cross-scope data visible")
	}
	noWarehouse := read(tenant, accountA, "other-moy")
	if noWarehouse[0].Warehouse.Status != "unmatched" || noWarehouse[0].Lines[0].Correlation.Status != "unmatched" {
		t.Fatal("different MoySklad account leaked mapping")
	}
	// A correlation is explicitly current. Changing the mapping does not mutate the observed line.
	if _, err := db.Pool.Exec(ctx, `UPDATE product_mappings SET target_id='new-target',version=2 WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id='1'`, tenant, accountA); err != nil {
		t.Fatal(err)
	}
	changed := read(tenant, accountA, moy)[0]
	if changed.Lines[0].Correlation.TargetID == nil || *changed.Lines[0].Correlation.TargetID != "new-target" || changed.Lines[0].OfferID != "matched" || changed.Lines[0].SKU != "11" {
		t.Fatal("current correlation or original observation incorrect")
	}
}

func TestPostingLegacyTimeAndCancellationUncertainty(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var batch int64
	if err := db.Pool.QueryRow(ctx, `INSERT INTO ozon_posting_batches(tenant_id,account_id,window_from,window_to,started_at,finished_at,traversal_status) VALUES($1,'seller',$2,$3,$2,$3,'completed') RETURNING id`, tenant, at, at.Add(time.Hour)).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO ozon_posting_identities(tenant_id,account_id,posting_number,order_id) VALUES($1,'seller','legacy','order')`, tenant); err != nil {
		t.Fatal(err)
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO ozon_posting_observations(batch_id,posting_number,order_id,order_number,status,substatus,warehouse_id,in_process_at,shipment_date,delivering_date,cancellation_reason,cancellation_type,cancellation_initiator,observed_at,tenant_id,account_id) VALUES($1,'legacy','order','order','active','','','','','','','','',$2,$3,'seller')`, batch, at.Add(time.Hour), tenant)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListPostings(ctx, domain.PostingReadScope{Tenant: tenant, Account: "seller", MoyskladAccount: "m"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("legacy read %+v %v", rows, err)
	}
	p := rows[0].Observation
	if p.ObservedTimeBasis != "batch_finished" || p.CancellationPresenceKnown || p.SourceCreatedAt != nil || p.SourceUpdatedAt != nil {
		t.Fatalf("legacy uncertainty lost %+v", p)
	}
}
