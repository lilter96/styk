//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

func rfbsStockFixture(t *testing.T, kind string) (*postgres.Store, domain.AutomaticStockPolicy) {
	t.Helper()
	db, _ := controlledFixtureWithMapping(t, false)
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	ctx, scope := context.Background(), testpublication.Scope()
	if _, err := db.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET warehouse_type=$1,is_rfbs=true WHERE warehouse_id=$2`, kind, testpublication.OzonWarehouse); err != nil {
		t.Fatal(err)
	}
	rfbsStockConfirmMappings(t, db)
	configured, err := db.ConfigureAutomaticStock(ctx, domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60,
		Targets:     []domain.AutomaticStockTargetRequest{{ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7}},
		Acknowledge: true, SessionID: "synthetic-rfbs", IdempotencyKey: "rfbs-stock-policy-001"}, time.Now())
	if err != nil || configured.Policy == nil || len(configured.Candidates) != 1 || !configured.Candidates[0].Eligible {
		t.Fatalf("rFBS policy or candidate rejected: %v", err)
	}
	rfbsStockRefresh(t, db, kind)
	return db, *configured.Policy
}

func rfbsStockConfirmMappings(t *testing.T, db *postgres.Store) {
	t.Helper()
	ctx, scope := context.Background(), testpublication.Scope()
	product, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount,
		ProductID: testpublication.OzonProduct, TargetKind: "product", TargetID: testpublication.SourceProduct, Evidence: domain.MappingOperatorAttested, SessionID: "synthetic-rfbs", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	confirm := domain.MappingConfirmationRequest{ReviewID: product.Review.ID, SessionID: "synthetic-rfbs", IdempotencyKey: "rfbs-product", Acknowledged: true, Explanation: "Synthetic rFBS mapping"}
	if _, err = db.ConfirmMappingReview(ctx, confirm); err != nil {
		t.Fatal(err)
	}
	warehouse, err := db.CreateWarehouseReview(ctx, domain.WarehouseReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount,
		MoyskladWarehouseID: testpublication.SourceWarehouse, OzonWarehouseID: testpublication.OzonWarehouse, SessionID: "synthetic-rfbs", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	confirm.ReviewID, confirm.IdempotencyKey = warehouse.Review.ID, "rfbs-warehouse"
	if _, err = db.ConfirmWarehouseReview(ctx, confirm); err != nil {
		t.Fatal(err)
	}
}

func rfbsStockRefresh(t *testing.T, db *postgres.Store, kind string) {
	t.Helper()
	source, market := rfbsStockSnapshots(kind)
	if _, err := db.SaveInventoryImport(context.Background(), source, "rfbs-source-fresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(context.Background(), market, "rfbs-market-fresh"); err != nil {
		t.Fatal(err)
	}
}

func rfbsStockSnapshots(kind string) (domain.InventoryImport, domain.OzonImport) {
	now, zero, seven, archived, rfbs := time.Now().UTC().Truncate(time.Microsecond), "0", "7", false, true
	source := domain.InventoryImport{Account: testpublication.MoyskladAccount, StartedAt: now, FinishedAt: now,
		Entities:       []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct}, {Kind: "store", ID: testpublication.SourceWarehouse}},
		WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &seven, Reserve: &zero, InTransit: &zero}}}
	market := domain.OzonImport{Account: testpublication.OzonAccount, StartedAt: now, FinishedAt: now,
		Products:   []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}},
		Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, WarehouseType: kind, Status: "created", IsRFBS: &rfbs}},
		Stocks:     []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, WarehouseID: testpublication.OzonWarehouse}}}
	return source, market
}

func TestRFBSStockUsesExistingEnrolledWorker(t *testing.T) {
	for _, kind := range []string{"rfbs", "rfbs_express"} {
		t.Run(kind, func(t *testing.T) {
			db, policy := rfbsStockFixture(t, kind)
			ctx := context.Background()
			review := rfbsReadyReview(t, db, policy)
			a := review.Attestations[0]
			a.Proofs = slices.Clone(a.Proofs)
			a.AutomaticPolicyRevision = 0
			for i := range a.Proofs {
				a.Proofs[i].ValidUntil = time.Now().Add(5 * time.Minute)
			}
			manual, err := db.PreviewStockSync(ctx, domain.StockSyncPreviewRequest{StockSyncScope: policy.StockSyncScope, Attestations: []domain.StockSyncAttestation{a}}, time.Now())
			if err != nil || manual.Ready {
				t.Fatal("rFBS stock bypassed target enrollment")
			}
			rfbsStockRun(t, db, review)
		})
	}
}

func rfbsReadyReview(t *testing.T, db *postgres.Store, policy domain.AutomaticStockPolicy) domain.StockSyncReview {
	t.Helper()
	ctx, imported := context.Background(), false
	for range 10 {
		if _, err := db.Pool.Exec(ctx, `UPDATE stock_auto_cycles SET next_action_at=clock_timestamp() WHERE state IN ('STARTING','READING')`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		view, err := db.AutomaticStockStatus(ctx, policy.StockSyncScope)
		if err != nil {
			t.Fatal(err)
		}
		if view.LastCycle == nil {
			continue
		}
		if view.LastCycle.State == "READING" && !imported {
			rfbsStockCommitReads(t, db, policy)
			imported = true
		}
		if view.LastCycle.PublicationID != "" {
			return rfbsStoredReview(t, db, view.LastCycle.PublicationID)
		}
		if view.LastCycle.State == "BLOCKED" {
			t.Fatalf("rFBS cycle blocked: %+v", view.LastCycle)
		}
	}
	t.Fatal("rFBS automatic cycle did not freeze a publication")
	return domain.StockSyncReview{}
}

func rfbsStoredReview(t *testing.T, db *postgres.Store, publication string) domain.StockSyncReview {
	t.Helper()
	var raw []byte
	if err := db.Pool.QueryRow(context.Background(), `SELECT r.snapshot FROM stock_publications p JOIN stock_sync_reviews r ON r.id=p.review_id WHERE p.id=$1`, publication).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var review domain.StockSyncReview
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	if !review.Ready || len(review.Targets) != 1 || review.Targets[0].Quantity == nil || *review.Targets[0].Quantity != "7" {
		t.Fatal("enrolled rFBS review not ready")
	}
	return review
}

func rfbsStockCommitReads(t *testing.T, db *postgres.Store, policy domain.AutomaticStockPolicy) {
	t.Helper()
	ctx := context.Background()
	var kind string
	if err := db.Pool.QueryRow(ctx, `SELECT warehouse_type FROM ozon_warehouse_observations WHERE warehouse_id=$1 ORDER BY import_id DESC LIMIT 1`, testpublication.OzonWarehouse).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		task, err := db.ClaimForCatalogWorker(ctx, time.Minute, policy.OzonAccount, policy.MoyskladAccount)
		if err != nil || task == nil {
			t.Fatalf("claim rFBS import: %v", err)
		}
		rfbsStockCommitRead(t, db, *task, kind)
	}
}

func rfbsStockCommitRead(t *testing.T, db *postgres.Store, task domain.Task, kind string) {
	t.Helper()
	source, market := rfbsStockSnapshots(kind)
	counts := domain.ImportCounts{Attempts: 1, Requests: 1, CompletedRequests: 1}
	if task.Type == domain.MoyskladCatalogTask {
		fingerprint, err := application.InventoryFingerprint(&source)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.CommitClaimedMoyskladImport(context.Background(), task, source, fingerprint, counts, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		return
	}
	fingerprint, err := application.OzonFingerprint(&market)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CommitClaimedOzonImport(context.Background(), task, market, fingerprint, counts, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func rfbsPublicationID(t *testing.T, db *postgres.Store, review domain.StockSyncReview) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM stock_publications WHERE review_id=$1`, review.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func rfbsStockRun(t *testing.T, db *postgres.Store, review domain.StockSyncReview) {
	t.Helper()
	publication := rfbsPublicationID(t, db, review)
	var writes atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/products/stocks" {
			rfbsStockAssertRequest(t, r)
			writes.Add(1)
			fixtureText(t, w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
			return
		}
		fixtureText(t, w, `{"products":[{"product_id":2764544165,"warehouse_id":1020001323428000,"offer_id":"ATEST-897677","present":9,"reserved":2,"free_stock":7}],"has_next":false}`)
	})
	runner := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
	if worked, err := runner.RunNext(context.Background()); err != nil || !worked {
		t.Fatalf("rFBS worker did not dispatch: %v", err)
	}
	if _, err := runner.Reconcile(context.Background(), review.StockSyncScope, publication); err != nil {
		t.Fatal(err)
	}
	result, err := db.StockPublication(context.Background(), review.StockSyncScope, publication)
	if err != nil || len(result.Targets) != 1 || result.Targets[0].State != "RECONCILED" || writes.Load() != 1 {
		t.Fatalf("rFBS publication or free-stock readback not reconciled: %v", err)
	}
}

func rfbsStockAssertRequest(t *testing.T, request *http.Request) {
	t.Helper()
	var body struct {
		Stocks []struct {
			ProductID   int64 `json:"product_id"`
			WarehouseID int64 `json:"warehouse_id"`
			Stock       int64 `json:"stock"`
		} `json:"stocks"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Stocks) != 1 {
		t.Error("invalid rFBS stock request")
		return
	}
	row := body.Stocks[0]
	if row.ProductID != 2764544165 || row.WarehouseID != 1020001323428000 || row.Stock != 7 {
		t.Error("rFBS stock request changed target or added reserved units to available stock")
	}
}

func TestRFBSStockRejectsUnallocatedSharedSourceWarehouse(t *testing.T) {
	db, policy := rfbsStockFixture(t, "rfbs")
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO ozon_warehouse_observations(import_id,warehouse_id,name,status,warehouse_type,is_rfbs) SELECT max(id),'1020001795665000','Synthetic second target','created','rfbs_express',true FROM source_imports WHERE source='ozon' AND account_id=$1`, policy.OzonAccount); err != nil {
		t.Fatal(err)
	}
	_, err := db.CreateWarehouseReview(ctx, domain.WarehouseReviewRequest{Tenant: policy.Tenant, MoyskladAccount: policy.MoyskladAccount, OzonAccount: policy.OzonAccount,
		MoyskladWarehouseID: testpublication.SourceWarehouse, OzonWarehouseID: "1020001795665000", SessionID: "synthetic-rfbs", Lifetime: time.Minute})
	if err == nil {
		t.Fatal("one physical source warehouse was allocated in full to a second delivery warehouse")
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_publication_targets`).Scan(&count); err != nil || count != 0 {
		t.Fatal("allocation conflict created a publication intent")
	}
}

func TestRFBSStockRevalidatesWarehouseBeforeDispatch(t *testing.T) {
	db, policy := rfbsStockFixture(t, "rfbs_express")
	review := rfbsReadyReview(t, db, policy)
	rfbsPublicationID(t, db, review)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE ozon_warehouse_observations SET status='disabled' WHERE warehouse_id=$1`, testpublication.OzonWarehouse); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); fixtureText(t, w, `{}`) })
	runner := rfbsStockRunner(db, client)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("disabled rFBS warehouse reached the provider")
	}
}

func rfbsStockRunner(db *postgres.Store, client *ozon.Client) stocksync.Runner {
	return stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
}

func TestRFBSStockProviderPermissionIsIndependentAndNeverReplayed(t *testing.T) {
	db, policy := rfbsStockFixture(t, "rfbs")
	review := rfbsReadyReview(t, db, policy)
	publication := rfbsPublicationID(t, db, review)
	var writes atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/products/stocks" {
			writes.Add(1)
			w.WriteHeader(http.StatusForbidden)
			fixtureText(t, w, `{}`)
			return
		}
		fixtureText(t, w, `{"products":[{"product_id":2764544165,"warehouse_id":1020001323428000,"offer_id":"ATEST-897677","free_stock":0}],"has_next":false}`)
	})
	runner := rfbsStockRunner(db, client)
	for range 2 {
		if _, err := runner.RunNext(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	result, err := db.StockPublication(context.Background(), review.StockSyncScope, publication)
	if err != nil || len(result.Targets) != 1 || result.Targets[0].State != "UNKNOWN" || writes.Load() != 1 {
		t.Fatal("warehouse capability granted provider permission or replayed an uncertain write")
	}
}
