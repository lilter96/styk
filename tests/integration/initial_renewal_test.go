//go:build integration

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestCancelledInitialPublicationRequiresFreshAuthorization(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx := context.Background()
	scope := testpublication.Scope()
	request := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, IntervalSeconds: 30, ValidMinutes: 60, Targets: []domain.AutomaticStockTargetRequest{{ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 7, InitialPublication: true}}, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "initial-before-pause-0001"}
	if _, err := db.ConfigureAutomaticStock(ctx, request, time.Now()); err != nil {
		t.Fatal(err)
	}
	first := initialCycle(t, db, scope, "WAITING_PUBLICATION", "")
	disable := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: false, Acknowledge: true, SessionID: "synthetic", IdempotencyKey: "initial-pause-before-send"}
	if _, err := db.ConfigureAutomaticStock(ctx, disable, time.Now()); err != nil {
		t.Fatal(err)
	}
	old, err := db.StockPublication(ctx, scope, first.PublicationID)
	if err != nil || old.Targets[0].State != "CANCELLED" || old.Targets[0].DispatchedAt != nil {
		t.Fatalf("not cancelled before send: %+v %v", old, err)
	}
	request.Targets[0].InitialPublication = false
	request.IdempotencyKey = "initial-resume-without-consent"
	if _, err = db.ConfigureAutomaticStock(ctx, request, time.Now()); err != nil {
		t.Fatal(err)
	}
	blocked := initialCycle(t, db, scope, "BLOCKED", first.ID)
	if blocked.Outcome != "EVIDENCE_BLOCKED" {
		t.Fatalf("missing explicit authorization was accepted: %+v", blocked)
	}
	request.Targets[0].InitialPublication = true
	request.IdempotencyKey = "initial-resume-new-consent"
	view, err := db.ConfigureAutomaticStock(ctx, request, time.Now())
	if err != nil || len(view.Candidates) != 1 || !view.Candidates[0].InitialEligible {
		t.Fatalf("safe renewal unavailable: %+v %v", view, err)
	}
	renewed := initialCycle(t, db, scope, "WAITING_PUBLICATION", blocked.ID)
	if renewed.PublicationID == first.PublicationID {
		t.Fatal("cancelled intent was reused")
	}
	var audits int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_initial_publications WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3`, scope.OzonAccount, testpublication.OzonProduct, testpublication.OzonWarehouse).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("initial intents not preserved: %d %v", audits, err)
	}
	rollbackEmptySaaSMigrations(t, db)
	cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "refusing to discard renewed initial publication history") {
		t.Fatal("renewed intent history could be removed")
	}
}

func initialCycle(t *testing.T, db *postgres.Store, scope domain.StockSyncScope, want, previous string) domain.AutomaticStockCycle {
	t.Helper()
	ctx := context.Background()
	var id string
	for i := 0; i < 100; i++ {
		if _, _, err := db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		v, err := db.AutomaticStockStatus(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if v.LastCycle != nil && v.LastCycle.ID != previous && v.LastCycle.State == "READING" {
			id = v.LastCycle.ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("cycle did not start")
	}
	commitInitialFakeImports(t, db, scope)
	for i := 0; i < 100; i++ {
		if _, _, err := db.AdvanceAutomaticStock(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		v, err := db.AutomaticStockStatus(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if v.LastCycle != nil && v.LastCycle.ID == id && v.LastCycle.State == want {
			return *v.LastCycle
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("cycle did not reach %s", want)
	return domain.AutomaticStockCycle{}
}

func commitInitialFakeImports(t *testing.T, db *postgres.Store, scope domain.StockSyncScope) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	zero, seven := "0", "7"
	fbs, archived := false, false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &seven, Reserve: &zero, InTransit: &zero}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, Status: "created", IsRFBS: &fbs}}}
	for i := 0; i < 2; i++ {
		task, err := db.ClaimForCatalogWorker(ctx, time.Minute, scope.OzonAccount, scope.MoyskladAccount)
		if err != nil || task == nil {
			t.Fatalf("claim synthetic import: %+v %v", task, err)
		}
		counts := domain.ImportCounts{Attempts: 1, Requests: 1, CompletedRequests: 1}
		if task.Type == domain.MoyskladCatalogTask {
			fingerprint, e := application.InventoryFingerprint(&source)
			if e != nil {
				t.Fatal(e)
			}
			err = db.CommitClaimedMoyskladImport(ctx, *task, source, fingerprint, counts, 5*time.Second)
		} else {
			fingerprint, e := application.OzonFingerprint(&market)
			if e != nil {
				t.Fatal(e)
			}
			err = db.CommitClaimedOzonImport(ctx, *task, market, fingerprint, counts, 5*time.Second)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
