//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/stocksync"
)

func TestStockConnectedAccountDispatchAndRecoveryKeepOneAttempt(t *testing.T) {
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	scope := domain.StockSyncScope{Tenant: "connected-merchant", MoyskladAccount: "connected-source", OzonAccount: "900998"}
	db, review := controlledFixtureForScope(t, true, scope, "27")
	view := controlledEnable(t, db, review)
	writes := 0
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/products/stocks" {
			writes++
			http.Error(w, "synthetic unavailable", 500)
			return
		}
		t.Errorf("unexpected endpoint %s", r.URL.Path)
	})
	runner := stocksync.Runner{Enabled: true, DurableDispatch: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveScopedOzon: func(_ context.Context, actual domain.StockSyncScope) (*ozon.Client, error) {
		if actual != scope {
			t.Error("resolver lost exact merchant account scope")
		}
		return client, nil
	}}
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := db.StockPublication(context.Background(), scope, view.LastOperation.ID)
	if err != nil || op.State != "UNKNOWN" || writes != 1 {
		t.Fatal("unknown provider outcome lost", err, writes)
	}
	if err = db.RecoverControlledStockPublications(context.Background(), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, err := db.NextControlledStockPublication(context.Background(), time.Now().Add(2*time.Minute)); err != nil || again != nil {
		t.Fatal("unknown write was retried", err)
	}
	if writes != 1 {
		t.Fatal("provider write repeated")
	}

	observed := int64(27)
	if err = db.RecordUnknownStockReadback(context.Background(), scope, op.ID, op.Targets[0].ID, &observed, time.Now()); err != nil {
		t.Fatal(err)
	}
	readback, err := db.StockPublication(context.Background(), scope, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.StockRecoveryRequest{StockSyncScope: scope, OperationID: op.ID, SessionID: "synthetic-owner", IdempotencyKey: "connected-recovery-001", Acknowledge: true, WorkerStopped: true, Reason: "Owner accepted the uncertain provider result after readback", ExpectedQuantity: "27", ExpectedReadbackAt: *readback.Targets[0].ReadbackAt}
	if _, err = db.SupersedeStockUnknown(context.Background(), request, time.Now()); err == nil {
		t.Fatal("active worker heartbeat did not block recovery")
	}
	if _, err = db.Pool.Exec(context.Background(), `UPDATE stock_test_worker_presence SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SupersedeStockUnknown(context.Background(), request, time.Now()); err != nil {
		t.Fatal("connected recovery remained allowlisted", err)
	}
	if _, err = db.SupersedeStockUnknown(context.Background(), request, time.Now()); err != nil {
		t.Fatal("recovery replay lost idempotency", err)
	}
	original, err := db.StockPublication(context.Background(), scope, op.ID)
	if err != nil || original.State != "UNKNOWN" || writes != 1 {
		t.Fatal("recovery changed or resent historical write", err)
	}
}

func TestStockConnectedAutomaticPolicySurvivesRefusedLegacyRollback(t *testing.T) {
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	scope := domain.StockSyncScope{Tenant: "connected-auto", MoyskladAccount: "connected-source", OzonAccount: "900997"}
	db, review := controlledFixtureForScope(t, true, scope, "27")
	target := review.Targets[0]
	request := domain.AutomaticStockRequest{StockSyncScope: scope, Enabled: true, Acknowledge: true, SessionID: "synthetic-owner", IdempotencyKey: "connected-auto-policy-001", IntervalSeconds: 30, ValidMinutes: 5, Targets: []domain.AutomaticStockTargetRequest{{ProductID: target.ProductID, WarehouseID: target.WarehouseID, SafetyStock: "0", Unreflected: "0", UnseenBound: "0", Maximum: 100}}}
	view, err := db.ConfigureAutomaticStock(context.Background(), request, time.Now())
	if err != nil || view.Policy == nil || !view.Policy.Enabled || view.Policy.Targets[0].Maximum != 100 {
		t.Fatal("connected automatic policy was restricted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := migrationCommand(t, ctx, "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down-to", "66")
	if _, err = cmd.CombinedOutput(); err == nil {
		t.Fatal("rollback restored an incompatible account constraint")
	}
	catalogSyncMigrate(t, db.Pool.Config().ConnString(), "up")
	after, err := db.AutomaticStockStatus(context.Background(), scope)
	if err != nil || after.Policy == nil || after.Policy.Revision != view.Policy.Revision || !after.Policy.Enabled {
		t.Fatal("refused rollback changed the policy", err)
	}
}
