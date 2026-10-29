//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestControlledUnknownSupersessionIsAuditedAndSerialized(t *testing.T) {
	db, oldReview := controlledFixture(t)
	ctx := context.Background()
	initial := controlledEnable(t, db, oldReview)
	base := time.Now().UTC().Truncate(time.Microsecond)
	claimed, err := db.NextControlledStockPublication(ctx, base)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	request := domain.StockRecoveryRequest{StockSyncScope: oldReview.StockSyncScope, OperationID: claimed.ID, SessionID: "synthetic-session-audit", IdempotencyKey: "synthetic-recovery-key-1", ExpectedReadbackAt: base.Add(time.Second), ExpectedQuantity: "0", Reason: "Accept uncertain outcome for synthetic test target", Acknowledge: true, WorkerStopped: true}
	if _, err = db.SupersedeTestUnknown(ctx, request, base.Add(2*time.Second)); !errors.Is(err, domain.ErrStockSyncBlocked) {
		t.Fatalf("active dispatch allowed: %v", err)
	}
	if err = db.FinishStockTarget(ctx, claimed.StockSyncScope, claimed.ID, claimed.Targets[0].ID, "UNKNOWN", "REQUEST_OUTCOME_UNKNOWN", base); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if err = db.RecordUnknownStockReadback(ctx, claimed.StockSyncScope, claimed.ID, claimed.Targets[0].ID, &zero, request.ExpectedReadbackAt); err != nil {
		t.Fatal(err)
	}
	if err = db.TouchTestStockWorker(ctx, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", base.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SupersedeTestUnknown(ctx, request, base.Add(3*time.Second)); !errors.Is(err, domain.ErrStockSyncBlocked) {
		t.Fatalf("active worker allowed: %v", err)
	}
	for _, bad := range []func(*domain.StockRecoveryRequest){func(r *domain.StockRecoveryRequest) { r.Acknowledge = false }, func(r *domain.StockRecoveryRequest) { r.WorkerStopped = false }, func(r *domain.StockRecoveryRequest) { r.Tenant = "another-tenant" }, func(r *domain.StockRecoveryRequest) { r.OzonAccount = "another-account" }} {
		v := request
		bad(&v)
		if _, e := db.SupersedeTestUnknown(ctx, v, base.Add(20*time.Second)); !errors.Is(e, domain.ErrStockSyncInvalid) {
			t.Fatalf("invalid authorization: %v", e)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := db.SupersedeTestUnknown(ctx, request, base.Add(20*time.Second)); e != nil {
				t.Errorf("idempotent concurrent recovery: %v", e)
			}
		}()
	}
	wg.Wait()
	var recoveries, fences, operations int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_publication_recoveries),(SELECT count(*) FROM stock_publication_target_fences),(SELECT count(*) FROM stock_publications)`).Scan(&recoveries, &fences, &operations); err != nil || recoveries != 1 || fences != 0 || operations != 1 {
		t.Fatalf("recovery caused publication or lost audit: %d %d %d %v", recoveries, fences, operations, err)
	}
	original, err := db.StockPublication(ctx, oldReview.StockSyncScope, initial.LastOperation.ID)
	if err != nil || original.State != "UNKNOWN" || original.Targets[0].ReadbackQuantity == nil || *original.Targets[0].ReadbackQuantity != "0" {
		t.Fatalf("original changed: %+v %v", original, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE stock_publication_targets SET safe_error='rewritten' WHERE id=$1`, original.Targets[0].ID); err == nil {
		t.Fatal("superseded original accepted mutation")
	}
	if _, err = db.MutateStockSync(ctx, oldReview.StockSyncScope, "enable", oldReview.ID, "old-review-test-key", base.Add(21*time.Second)); !errors.Is(err, domain.ErrStockSyncBlocked) && !errors.Is(err, domain.ErrStockSyncStale) {
		t.Fatalf("old review reused: %v", err)
	}
	// Both providers are traversed after acceptance; values are synthetic and
	// evidence is operator-attested, as it will be in the live test scope.
	stock, reserve, incoming := "7", "0", "0"
	archived, rfbs := false, false
	source := domain.InventoryImport{Account: request.MoyskladAccount, StartedAt: base.Add(22 * time.Second), FinishedAt: base.Add(23 * time.Second), Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct, Article: "synthetic"}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &stock, Reserve: &reserve, InTransit: &incoming}}}
	market := domain.OzonImport{Account: request.OzonAccount, StartedAt: base.Add(22 * time.Second), FinishedAt: base.Add(23 * time.Second), Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, Status: "created", IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, WarehouseID: testpublication.OzonWarehouse, Present: 0, Reserved: 0, FreeStock: 0}}}
	if _, err = db.SaveInventoryImport(ctx, source, "recovery-synthetic-source"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveOzonImport(ctx, market, "recovery-synthetic-market"); err != nil {
		t.Fatal(err)
	}
	attestations := oldReview.Attestations
	for i := range attestations {
		for k := range attestations[i].Proofs {
			attestations[i].Proofs[k].AsOf = base.Add(21 * time.Second)
			attestations[i].Proofs[k].ValidUntil = base.Add(5 * time.Minute)
		}
	}
	review, err := db.PreviewStockSync(ctx, domain.StockSyncPreviewRequest{StockSyncScope: request.StockSyncScope, Attestations: attestations}, base.Add(24*time.Second))
	if err != nil || !review.Ready {
		t.Fatalf("fresh review: %+v %+v %v", review.Checks, review.Targets, err)
	}
	var results [2]domain.StockSyncView
	var errs [2]error
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = db.MutateStockSync(ctx, request.StockSyncScope, "enable", review.ID, "new-publication-key-1", base.Add(25*time.Second))
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].LastOperation == nil || results[1].LastOperation == nil || results[0].LastOperation.ID != results[1].LastOperation.ID || results[0].LastOperation.ID == claimed.ID {
		t.Fatalf("duplicate new intent: %+v %+v %v %v", results[0].LastOperation, results[1].LastOperation, errs[0], errs[1])
	}
	var linked string
	if err = db.Pool.QueryRow(ctx, `SELECT new_operation_id FROM stock_publication_recovery_links`).Scan(&linked); err != nil || linked != results[0].LastOperation.ID {
		t.Fatalf("missing immutable recovery link: %s %v", linked, err)
	}
}

func TestControlledUnknownDistinctConcurrentRecoveriesOnlyOneAccepted(t *testing.T) {
	db, review := controlledFixture(t)
	ctx := context.Background()
	controlledEnable(t, db, review)
	now := time.Now().UTC().Truncate(time.Microsecond)
	claim, err := db.NextControlledStockPublication(ctx, now)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = db.FinishStockTarget(ctx, review.StockSyncScope, claim.ID, claim.Targets[0].ID, "UNKNOWN", "REQUEST_OUTCOME_UNKNOWN", now); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if err = db.RecordUnknownStockReadback(ctx, review.StockSyncScope, claim.ID, claim.Targets[0].ID, &zero, now); err != nil {
		t.Fatal(err)
	}
	request := domain.StockRecoveryRequest{StockSyncScope: review.StockSyncScope, OperationID: claim.ID, SessionID: "synthetic-session", ExpectedReadbackAt: now, ExpectedQuantity: "0", Reason: "Synthetic acceptance of uncertain prior outcome", Acknowledge: true, WorkerStopped: true}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := request
			r.IdempotencyKey = fmt.Sprintf("distinct-recovery-key-%d", i)
			_, e := db.SupersedeTestUnknown(ctx, r, now.Add(time.Second))
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	accepted, blocked := 0, 0
	for e := range results {
		if e == nil {
			accepted++
		} else if errors.Is(e, domain.ErrStockSyncBlocked) {
			blocked++
		} else {
			t.Errorf("unexpected recovery error: %v", e)
		}
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_publication_recoveries`).Scan(&count); err != nil || accepted != 1 || blocked != 7 || count != 1 {
		t.Fatalf("accepted=%d blocked=%d events=%d err=%v", accepted, blocked, count, err)
	}
}
