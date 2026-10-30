//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

type stockFixtureData struct {
	store  *postgres.Store
	req    domain.StockCalculationRequest
	source domain.InventoryImport
	ozon   domain.OzonImport
}

func newStockFixture(t *testing.T) stockFixtureData { return newStockFixtureWithHistory(t, true) }

func newStockFixtureWithHistory(t *testing.T, includeHistory bool) stockFixtureData {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	r := domain.StockCalculationRequest{
		Pool:   domain.StockPool{Tenant: "stock-t-" + suffix, MoyskladAccount: "stock-m-" + suffix, WarehouseID: "warehouse", ItemKind: "variant", ItemID: "item"},
		Target: domain.StockTarget{OzonAccount: "stock-o-" + suffix, WarehouseID: "ozon-warehouse", ProductID: "product"},
		Policy: domain.StockCalculationPolicy{Version: 1, Unit: "piece", SafetyStock: "1", MaxObservationAge: time.Hour, DecisionLifetime: 5 * time.Minute},
	}
	now := time.Now().UTC().Add(-time.Minute)
	stock, reserve, incoming := "10", "1", "0"
	ms := domain.InventoryImport{Account: r.Pool.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now,
		Entities:       []domain.ExternalEntity{{Kind: "variant", ID: "item", Article: "offer"}, {Kind: "store", ID: "warehouse"}},
		WarehouseStock: []domain.WarehouseStockObservation{{Kind: "variant", ExternalID: "item", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming}},
	}
	rfbs := false
	oz := domain.OzonImport{Account: r.Target.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now,
		Products:   []domain.OzonProduct{{ProductID: "product", OfferID: "offer", SKU: "sku"}},
		Warehouses: []domain.OzonWarehouse{{ID: "ozon-warehouse", IsRFBS: &rfbs}},
		Stocks:     []domain.OzonStock{{ProductID: "product", OfferID: "offer", SKU: "sku", WarehouseID: "ozon-warehouse", Present: 5, Reserved: 1, FreeStock: 4}},
	}
	if _, err = db.SaveInventoryImport(ctx, ms, "source-fingerprint"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveOzonImport(ctx, oz, "ozon-fingerprint"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ source, account string }{{"moysklad", r.Pool.MoyskladAccount}, {"ozon", r.Target.OzonAccount}} {
		if err = db.RegisterStockConnection(ctx, r.Pool.Tenant, c.source, c.account, "fixture ownership attestation", includeHistory); err != nil {
			t.Fatal(err)
		}
	}
	scope := postgres.MatchScope{Tenant: r.Pool.Tenant, MoyskladAccount: r.Pool.MoyskladAccount, OzonAccount: r.Target.OzonAccount}
	if _, err = db.ConfirmMatch(ctx, scope, "product", "variant", "item", "fixture product confirmation"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmWarehouseMapping(ctx, scope, "warehouse", "ozon-warehouse", "fixture warehouse confirmation"); err != nil {
		t.Fatal(err)
	}
	return stockFixtureData{db, r, ms, oz}
}

func stockCalc(t *testing.T, f stockFixtureData) domain.StockDecision {
	t.Helper()
	d, err := (application.StockCalculator{Store: f.store}).Run(context.Background(), f.req, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func stockHasReason(d domain.StockDecision, code string) bool {
	i := sort.SearchStrings(d.ReasonCodes, code)
	return i < len(d.ReasonCodes) && d.ReasonCodes[i] == code
}

func TestStockImportedEvidenceIsUnknownAndImmutable(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	var tasksBefore int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&tasksBefore); err != nil {
		t.Fatal(err)
	}
	d := stockCalc(t, f)
	if d.Status != domain.StockUnknown || d.Quantity != nil || d.ExpiresAt != nil || !d.ListingMayBeExposed {
		t.Fatalf("unsafe imported decision: %+v", d)
	}
	for _, reason := range []string{commitment.ProofBaseline, commitment.ProofAttribution, commitment.ProofUnassigned, commitment.ProofPostingCoverage, commitment.ProofCompeting, commitment.ProofSourceOrdering} {
		if !stockHasReason(d, reason) {
			t.Errorf("missing reason %s", reason)
		}
	}
	if d.Evidence.Source.ImportID == nil || !d.Evidence.Source.ObservationFound || *d.Evidence.Source.Physical != "10" || *d.Evidence.Source.Reserve != "1" || d.Evidence.Source.TimeBasis != "local_batch_finished" || d.Evidence.ProductMapping == nil || d.Evidence.WarehouseMapping == nil || !d.Evidence.Marketplace.StockRowFound || *d.Evidence.Marketplace.FreeStock != 4 {
		t.Fatalf("missing frozen evidence: %+v", d.Evidence)
	}
	read, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, d.ID)
	if err != nil || !stockDecisionsEqual(t, d, read) {
		t.Fatalf("decision replay mismatch: %v", err)
	}
	var storedAt time.Time
	if err = f.store.Pool.QueryRow(ctx, `SELECT evaluated_at FROM stock_decisions WHERE id=$1`, d.ID).Scan(&storedAt); err != nil || !storedAt.Equal(d.EvaluatedAt) {
		t.Fatalf("indexed and frozen decision times differ: %v", err)
	}
	for _, q := range []string{`UPDATE stock_decisions SET status='BLOCKED' WHERE id=$1`, `DELETE FROM stock_decision_evidence WHERE decision_id=$1`, `DELETE FROM stock_decisions WHERE id=$1`} {
		if _, err = f.store.Pool.Exec(ctx, q, d.ID); err == nil {
			t.Fatalf("immutable decision accepted: %s", q)
		}
	}
	if _, err = f.store.Pool.Exec(ctx, `DELETE FROM source_imports WHERE id=$1`, *d.Evidence.Source.ImportID); err == nil {
		t.Fatal("referenced observation import was deleted")
	}
	// Legacy source rows are mutable by privileged SQL; the decision snapshot
	// must preserve what was actually considered at calculation time.
	if _, err = f.store.Pool.Exec(ctx, `UPDATE source_warehouse_stock_observations SET stock=2 WHERE import_id=$1 AND kind=$2 AND external_id=$3 AND warehouse_id=$4`, *d.Evidence.Source.ImportID, f.req.Pool.ItemKind, f.req.Pool.ItemID, f.req.Pool.WarehouseID); err != nil {
		t.Fatal(err)
	}
	read, err = f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, d.ID)
	if err != nil || !stockDecisionsEqual(t, d, read) {
		t.Fatal("legacy observation update altered historical decision")
	}
	var outbox int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&outbox); err != nil || outbox != tasksBefore {
		t.Fatalf("external task scheduled: %d %v", outbox, err)
	}
}

func TestStockReportedZeroStillNeedsProof(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	zero := "0"
	f.source.WarehouseStock[0].Stock = &zero
	f.source.WarehouseStock[0].Reserve = &zero
	if _, err := f.store.SaveInventoryImport(ctx, f.source, "source-zero"); err != nil {
		t.Fatal(err)
	}
	d := stockCalc(t, f)
	if d.Status != domain.StockUnknown || d.Quantity != nil || d.Evidence.Source.Physical == nil || *d.Evidence.Source.Physical != "0" || !stockHasReason(d, commitment.ProofBaseline) {
		t.Fatalf("reported zero was treated as a ready decision: %+v", d)
	}
}

func TestStockUnconfirmedAndSharedMappings(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	unconfirmed := f.req
	unconfirmed.Pool.ItemID = "unmapped-item"
	unconfirmed.Target.ProductID = "unconfirmed-product"
	d, err := f.store.CalculateStock(ctx, unconfirmed, time.Now().UTC())
	if err != nil || d.Status != domain.StockUnknown || d.Quantity != nil || !stockHasReason(d, commitment.ProofProductMapping) {
		t.Fatalf("unconfirmed suggestion became a mapping: %+v %v", d, err)
	}
	var mappingID int64
	err = f.store.Pool.QueryRow(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,'second-product','second-offer',$3,$4,$5,1,'fixture shared confirmation') RETURNING id`, f.req.Pool.Tenant, f.req.Target.OzonAccount, f.req.Pool.MoyskladAccount, f.req.Pool.ItemKind, f.req.Pool.ItemID).Scan(&mappingID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Pool.Exec(ctx, `INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id) SELECT id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,(SELECT id FROM source_imports WHERE source='ozon' AND account_id=$2 ORDER BY id DESC LIMIT 1),(SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$3 ORDER BY id DESC LIMIT 1) FROM product_mappings WHERE id=$1`, mappingID, f.req.Target.OzonAccount, f.req.Pool.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	shared := stockCalc(t, f)
	if shared.Status != domain.StockBlocked || shared.Quantity != nil || !stockHasReason(shared, "SHARED_POOL_UNSUPPORTED") {
		t.Fatalf("shared target not blocked: %+v", shared)
	}
}

func TestStockConcurrentOwnershipClaim(t *testing.T) {
	f := newStockFixture(t)
	account := "contested-" + f.req.Pool.MoyskladAccount
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, tenant := range []string{f.req.Pool.Tenant, f.req.Pool.Tenant + "-other"} {
		wg.Add(1)
		go func(tenant string) {
			defer wg.Done()
			errs <- f.store.RegisterStockConnection(context.Background(), tenant, "moysklad", account, "explicit fixture claim", false)
		}(tenant)
	}
	wg.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("ownership claims succeeded: %d", succeeded)
	}
}

func TestStockRegistrationDoesNotAdoptOldImports(t *testing.T) {
	f := newStockFixtureWithHistory(t, false)
	ctx := context.Background()
	d := stockCalc(t, f)
	if d.Evidence.Source.ImportID != nil || d.Evidence.Marketplace.ImportID != nil || d.Evidence.Source.ObservationFound || d.Status != domain.StockUnknown {
		t.Fatalf("pre-registration imports were silently adopted: %+v", d.Evidence)
	}
	if err := f.store.RegisterStockConnection(ctx, f.req.Pool.Tenant, "moysklad", f.req.Pool.MoyskladAccount, "fixture ownership attestation", true); err == nil {
		t.Fatal("registration history scope was changed silently")
	}
	f.source.StartedAt, f.source.FinishedAt = time.Now().UTC(), time.Now().UTC()
	if _, err := f.store.SaveInventoryImport(ctx, f.source, "after-registration"); err != nil {
		t.Fatal(err)
	}
	newDecision := stockCalc(t, f)
	if newDecision.Evidence.Source.ImportID == nil || !newDecision.Evidence.Source.ObservationFound || newDecision.Status != domain.StockUnknown {
		t.Fatalf("post-registration import was not considered safely: %+v", newDecision.Evidence)
	}
}

func TestStockWarehouseConflictDoesNotExposeOtherTenant(t *testing.T) {
	f, other := newStockFixture(t), newStockFixture(t)
	ctx := context.Background()
	_, err := f.store.Pool.Exec(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance) VALUES($1,$2,$3,$4,'other-warehouse','active',1,'fixture')`, other.req.Pool.Tenant, f.req.Pool.MoyskladAccount, other.req.Target.OzonAccount, f.req.Pool.WarehouseID)
	if err == nil {
		t.Fatal("cross-tenant warehouse mapping was accepted")
	}
	d := stockCalc(t, f)
	if stockHasReason(d, "WAREHOUSE_MAPPING_CONFLICT") || d.Quantity != nil {
		t.Fatalf("rejected foreign mapping affected calculation: %+v", d)
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), other.req.Pool.Tenant) || strings.Contains(string(data), other.req.Target.OzonAccount) {
		t.Fatal("other tenant's mapping identity leaked")
	}
}

func TestStockDatabaseRejectsReadyWithoutDurableProof(t *testing.T) {
	f := newStockFixture(t)
	d := stockCalc(t, f)
	_, err := f.store.Pool.Exec(context.Background(), `INSERT INTO stock_decisions(tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,expires_at,status,quantity,decision) SELECT tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,evaluated_at+interval '1 minute','READY_CONDITIONAL',1,decision FROM stock_decisions WHERE id=$1`, d.ID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
		t.Fatalf("database accepted unsupported READY: %v", err)
	}
}

func TestStockPolicyVersionsAreImmutable(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	first := stockCalc(t, f)
	f.req.Policy.SafetyStock = "2"
	if _, err := f.store.CalculateStock(ctx, f.req, time.Now().UTC()); err == nil {
		t.Fatal("one policy version accepted different settings")
	}
	f.req.Policy.Version++
	second := stockCalc(t, f)
	if second.Status != domain.StockUnknown || second.Request.Policy.Version == first.Request.Policy.Version || second.Request.Policy.SafetyStock != "2" {
		t.Fatal("new policy version was not preserved")
	}
	if _, err := f.store.Pool.Exec(ctx, `UPDATE stock_policies SET safety_stock='3' WHERE tenant_id=$1 AND version=1`, f.req.Pool.Tenant); err == nil {
		t.Fatal("policy history was mutable")
	}
	read, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, first.ID)
	if err != nil || !stockDecisionsEqual(t, first, read) {
		t.Fatal("old policy or decision changed")
	}
}

func TestStockDecisionRequiresEvidence(t *testing.T) {
	f := newStockFixture(t)
	d := stockCalc(t, f)
	ctx := context.Background()
	tx, err := f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO stock_decisions(tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,status,decision) SELECT tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,status,decision FROM stock_decisions WHERE id=$1 RETURNING id`, d.ID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	var pgerr *pgconn.PgError
	if err = tx.Commit(ctx); !errors.As(err, &pgerr) || pgerr.Code != "23514" {
		t.Fatalf("decision without evidence committed: %v", err)
	}
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_decisions WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed decision remained: %d %v", count, err)
	}
}

func TestStockMissingMappingVersionIsBlocked(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	first := stockCalc(t, f)
	if _, err := f.store.Pool.Exec(ctx, `UPDATE product_mappings SET version=2 WHERE tenant_id=$1 AND ozon_product_id=$2`, f.req.Pool.Tenant, f.req.Target.ProductID); err != nil {
		t.Fatal(err)
	}
	later := stockCalc(t, f)
	if later.Status != domain.StockBlocked || later.Quantity != nil || later.Evidence.ProductMapping != nil || !stockHasReason(later, "PRODUCT_MAPPING_CONFLICT") {
		t.Fatalf("missing mapping history accepted: %+v", later)
	}
	old, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, first.ID)
	if err != nil || !stockDecisionsEqual(t, first, old) {
		t.Fatal("prior version was not preserved")
	}
}

func TestStockOwnershipAndScopedEvidence(t *testing.T) {
	f, other := newStockFixture(t), newStockFixture(t)
	ctx := context.Background()
	if err := f.store.RegisterStockConnection(ctx, other.req.Pool.Tenant, "moysklad", f.req.Pool.MoyskladAccount, "other tenant", false); err == nil {
		t.Fatal("two tenants claimed one account")
	}
	unregistered := f.req
	unregistered.Pool.Tenant = "unregistered"
	if _, err := f.store.CalculateStock(ctx, unregistered, time.Now().UTC()); err == nil {
		t.Fatal("caller-supplied tenant inferred ownership")
	}
	d := stockCalc(t, f)
	d2 := stockCalc(t, other)
	if _, err := f.store.StockDecision(ctx, other.req.Pool.Tenant, f.req.Target.OzonAccount, d.ID); err == nil {
		t.Fatal("cross-tenant decision read")
	}
	if _, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, other.req.Target.OzonAccount, d.ID); err == nil {
		t.Fatal("cross-account decision read")
	}
	// Create a new decision in a transaction, then attempt to attach another
	// tenant's evidence. The scoped FK must reject the insertion.
	for _, mode := range []string{"tenant", "moysklad-account", "ozon-account"} {
		t.Run(mode, func(t *testing.T) {
			tx, err := f.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var id int64
			err = tx.QueryRow(ctx, `INSERT INTO stock_decisions(tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,offer_id,product_mapping_id,product_mapping_version,warehouse_mapping_id,warehouse_mapping_version,policy_version,evaluated_at,status,decision) SELECT tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,offer_id,product_mapping_id,product_mapping_version,warehouse_mapping_id,warehouse_mapping_version,policy_version,evaluated_at,status,decision FROM stock_decisions WHERE id=$1 RETURNING id`, d.ID).Scan(&id)
			if err != nil {
				t.Fatal(err)
			}
			query := `INSERT INTO stock_decision_evidence(decision_id,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,moysklad_import_id,moysklad_stock_import_id,ozon_import_id,ozon_stock_import_id,posting_batch_id,snapshot) SELECT $1,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,moysklad_import_id,moysklad_stock_import_id,ozon_import_id,ozon_stock_import_id,posting_batch_id,snapshot FROM stock_decision_evidence WHERE decision_id=$2`
			switch mode {
			case "tenant":
				query = `INSERT INTO stock_decision_evidence(decision_id,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,snapshot) SELECT $1,$3,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,snapshot FROM stock_decision_evidence WHERE decision_id=$2`
				_, err = tx.Exec(ctx, query, id, d.ID, other.req.Pool.Tenant)
			case "moysklad-account":
				query = `INSERT INTO stock_decision_evidence(decision_id,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,moysklad_import_id,moysklad_stock_import_id,snapshot) SELECT $1,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,$3,$3,snapshot FROM stock_decision_evidence WHERE decision_id=$2`
				_, err = tx.Exec(ctx, query, id, d.ID, *d2.Evidence.Source.ImportID)
			case "ozon-account":
				query = `INSERT INTO stock_decision_evidence(decision_id,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,ozon_import_id,ozon_stock_import_id,snapshot) SELECT $1,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,$3,$3,snapshot FROM stock_decision_evidence WHERE decision_id=$2`
				_, err = tx.Exec(ctx, query, id, d.ID, *d2.Evidence.Marketplace.ImportID)
			}
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
				t.Fatalf("cross-scope evidence accepted: %v", err)
			}
		})
	}
}

func TestStockRepeatedImportAndMissingObservation(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	before := stockCalc(t, f)
	f.source.StartedAt, f.source.FinishedAt = time.Now(), time.Now()
	created, err := f.store.SaveInventoryImport(ctx, f.source, "source-fingerprint")
	if err != nil || created {
		t.Fatalf("identical import not deduplicated: %v %v", created, err)
	}
	f.req.Policy.MaxObservationAge = time.Second
	f.req.Policy.Version = 2
	stale := stockCalc(t, f)
	if !stale.Evidence.Source.ObservedAt.Equal(*before.Evidence.Source.ObservedAt) || !stockHasReason(stale, "SOURCE_OBSERVATION_STALE") || stale.Quantity != nil {
		t.Fatal("deduplicated observation incorrectly refreshed")
	}
	f.source.WarehouseStock = nil
	if _, err = f.store.SaveInventoryImport(ctx, f.source, "later-missing"); err != nil {
		t.Fatal(err)
	}
	missing := stockCalc(t, f)
	if missing.Evidence.Source.ObservationFound || missing.Evidence.Source.Physical != nil || !stockHasReason(missing, "SOURCE_OBSERVATION_MISSING") || missing.Quantity != nil {
		t.Fatalf("missing row treated as stock: %+v", missing)
	}
	previous, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, before.ID)
	if err != nil || !stockDecisionsEqual(t, previous, before) {
		t.Fatal("prior evidence changed")
	}
}

func TestStockPostingsNeverManufactureCommitments(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	line := domain.PostingLine{OfferID: "offer", SKU: "sku", Quantity: 2}
	active := postingFixture("posting-1", "awaiting_packaging", now, line)
	active.WarehouseID = f.req.Target.WarehouseID
	split := postingFixture("posting-2", "awaiting_packaging", now, line)
	split.ParentPostingNumber = "posting-1"
	cancelled := postingFixture("posting-1", "cancelled", now.Add(time.Minute), domain.PostingLine{OfferID: "offer", SKU: "sku", Quantity: 1})
	reordered := postingFixture("posting-1", "awaiting_packaging", now.Add(-time.Minute), line)
	for _, batch := range []domain.PostingImport{
		postingBatch(f.req.Pool.Tenant, f.req.Target.OzonAccount, now, active, split),
		postingBatch(f.req.Pool.Tenant, f.req.Target.OzonAccount, now, cancelled),
		postingBatch(f.req.Pool.Tenant, f.req.Target.OzonAccount, now, cancelled),
		postingBatch(f.req.Pool.Tenant, f.req.Target.OzonAccount, now, reordered),
	} {
		if _, err := f.store.SavePostings(ctx, batch); err != nil {
			t.Fatal(err)
		}
		d := stockCalc(t, f)
		if d.Status != domain.StockUnknown || d.Quantity != nil || d.Evidence.Unreflected != nil || !stockHasReason(d, commitment.ProofAttribution) || !stockHasReason(d, commitment.ProofPostingCoverage) || d.Evidence.Marketplace.PostingBatchID == nil {
			t.Fatalf("posting inferred a commitment or release: %+v", d)
		}
	}
}

func TestStockConflictsAndMappingVersionHistory(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	first := stockCalc(t, f)
	// A later current mapping conflict must not rewrite the earlier decision.
	_, err := f.store.Pool.Exec(ctx, `UPDATE product_mappings SET target_id='other-item' WHERE tenant_id=$1 AND ozon_product_id=$2`, f.req.Pool.Tenant, f.req.Target.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	blocked := stockCalc(t, f)
	if blocked.Status != domain.StockBlocked || blocked.Quantity != nil || !stockHasReason(blocked, "PRODUCT_MAPPING_CONFLICT") || blocked.Evidence.ProductMapping != nil {
		t.Fatalf("mapping conflict not blocked: %+v", blocked)
	}
	prior, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, first.ID)
	if err != nil || !stockDecisionsEqual(t, first, prior) {
		t.Fatal("mapping change rewrote historical decision")
	}
	variantMismatch := f.req
	variantMismatch.Pool.ItemKind = "product"
	d, err := f.store.CalculateStock(ctx, variantMismatch, time.Now().UTC())
	if err != nil || d.Status != domain.StockBlocked || !stockHasReason(d, "PRODUCT_MAPPING_CONFLICT") {
		t.Fatalf("product/variant mismatch: %+v %v", d, err)
	}
}

func TestStockDecisionRollbackAndConstraint(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	first := stockCalc(t, f)
	constraint := "test_stock_evidence_failure"
	q := fmt.Sprintf(`ALTER TABLE stock_decision_evidence ADD CONSTRAINT %s CHECK (tenant_id <> '%s') NOT VALID`, constraint, f.req.Pool.Tenant)
	if _, err := f.store.Pool.Exec(ctx, q); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.store.Pool.Exec(context.Background(), `ALTER TABLE stock_decision_evidence DROP CONSTRAINT IF EXISTS test_stock_evidence_failure`); err != nil {
			t.Error(err)
		}
	})
	failing := f.req
	failing.Pool.WarehouseID = "new-pool-before-failure"
	if _, err := f.store.CalculateStock(ctx, failing, time.Now().UTC()); err == nil {
		t.Fatal("injected evidence failure was ignored")
	}
	var decisions, evidence, newPools int
	if err := f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),(SELECT count(*) FROM stock_decision_evidence WHERE tenant_id=$1),(SELECT count(*) FROM stock_pools WHERE tenant_id=$1 AND warehouse_id=$2)`, f.req.Pool.Tenant, failing.Pool.WarehouseID).Scan(&decisions, &evidence, &newPools); err != nil || decisions != 1 || evidence != 1 || newPools != 0 {
		t.Fatalf("partial decision after rollback: %d %d %d %v", decisions, evidence, newPools, err)
	}
	read, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, first.ID)
	if err != nil || !stockDecisionsEqual(t, first, read) {
		t.Fatal("prior decision lost after rollback")
	}
	if _, err = f.store.Pool.Exec(ctx, `ALTER TABLE stock_decision_evidence DROP CONSTRAINT test_stock_evidence_failure`); err != nil {
		t.Fatal(err)
	}
	if later := stockCalc(t, f); later.ID == first.ID || later.Status != domain.StockUnknown {
		t.Fatal("retry did not append decision")
	}
}

func TestStockConcurrentCalculations(t *testing.T) {
	f := newStockFixture(t)
	const workers = 4
	results := make(chan domain.StockDecision, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := f.store.CalculateStock(context.Background(), f.req, time.Now().UTC())
			if err != nil {
				errs <- err
				return
			}
			results <- d
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Errorf("concurrent calculation: %v", err)
	}
	seen := make(map[int64]bool)
	for d := range results {
		if d.Status != domain.StockUnknown || d.Quantity != nil || seen[d.ID] {
			t.Fatalf("invalid concurrent decision: %+v", d)
		}
		seen[d.ID] = true
	}
	if len(seen) != workers {
		t.Fatalf("got %d decisions, want %d", len(seen), workers)
	}
	var pools int
	if err := f.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM stock_pools WHERE tenant_id=$1`, f.req.Pool.Tenant).Scan(&pools); err != nil || pools != 1 {
		t.Fatalf("duplicate physical pools: %d %v", pools, err)
	}
}

func TestStockMappingChangeDuringCalculation(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	before := stockCalc(t, f)
	tx, err := f.store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "matching:"+f.req.Pool.Tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE product_mappings SET target_id='changed-during-calculation' WHERE tenant_id=$1`, f.req.Pool.Tenant); err != nil {
		t.Fatal(err)
	}
	type result struct {
		d   domain.StockDecision
		err error
	}
	ch := make(chan result, 1)
	go func() {
		d, e := f.store.CalculateStock(context.Background(), f.req, time.Now().UTC())
		ch <- result{d, e}
	}()
	// Wait for the calculation to block on the held advisory lock.
	deadline := time.Now().Add(5 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = f.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event='advisory' AND query LIKE '%pg_advisory_xact_lock%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("calculation did not reach held lock")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.d.Status != domain.StockBlocked || !stockHasReason(got.d, "PRODUCT_MAPPING_CONFLICT") || got.d.Evidence.ProductMapping != nil {
		t.Fatalf("calculation mixed mapping versions: %+v", got.d)
	}
	old, err := f.store.StockDecision(ctx, f.req.Pool.Tenant, f.req.Target.OzonAccount, before.ID)
	if err != nil || !stockDecisionsEqual(t, before, old) {
		t.Fatal("historical decision changed")
	}
}

// Persistence freezes the JSON contract. time.Location and monotonic clock
// internals are not serialized and must not turn equal snapshots into failures.
func stockDecisionsEqual(t testing.TB, a, b domain.StockDecision) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(left, right)
}
