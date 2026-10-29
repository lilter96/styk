//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
	"github.com/lilter96/styk/internal/testpublication"
)

// Each case has its own migrated schema. These identifiers deliberately equal
// the live allowlist, but the database and transport must be strictly local.
func controlledFixture(t *testing.T) (*postgres.Store, domain.StockSyncReview) {
	return controlledFixtureWithMapping(t, true)
}

func controlledFixtureWithMapping(t *testing.T, mapping bool) (*postgres.Store, domain.StockSyncReview) {
	t.Helper()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || !strings.Contains(u.Path, "_test") {
		t.Skip("dedicated local test database required")
	}
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	ctx := context.Background()
	admin, err := postgres.Open(ctx, u.String(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("controlled_%d", time.Now().UnixNano())
	if _, err = admin.Pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Pool.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); admin.Pool.Close() })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", u.String(), "up")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("migrate isolated schema: %v\n%s", e, output)
	}
	db, err := postgres.Open(ctx, u.String(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	scope := testpublication.Scope()
	for _, c := range []struct{ p, a string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err = db.RegisterStockConnection(ctx, scope.Tenant, c.p, c.a, "synthetic owner registration", true); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	stock, zero, incoming := "7", "0", "0"
	fbs, archived := false, false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct, Article: "synthetic"}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &stock, Reserve: &zero, InTransit: &incoming}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, Status: "created", IsRFBS: &fbs}}, Stocks: []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, WarehouseID: testpublication.OzonWarehouse, Present: 0, Reserved: 0, FreeStock: 0}}}
	if _, err = db.SaveInventoryImport(ctx, source, "controlled-synthetic-source"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveOzonImport(ctx, market, "controlled-synthetic-market"); err != nil {
		t.Fatal(err)
	}
	if !mapping {
		return db, domain.StockSyncReview{}
	}
	pr, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, ProductID: testpublication.OzonProduct, TargetKind: "product", TargetID: testpublication.SourceProduct, Evidence: domain.MappingOperatorAttested, SessionID: "synthetic-session", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	confirm := domain.MappingConfirmationRequest{ReviewID: pr.Review.ID, SessionID: "synthetic-session", IdempotencyKey: "synthetic-product", Acknowledged: true, Explanation: "Synthetic controlled fixture"}
	if _, err = db.ConfirmMappingReview(ctx, confirm); err != nil {
		t.Fatal(err)
	}
	wr, err := db.CreateWarehouseReview(ctx, domain.WarehouseReviewRequest{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount, MoyskladWarehouseID: testpublication.SourceWarehouse, OzonWarehouseID: testpublication.OzonWarehouse, SessionID: "synthetic-session", Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	confirm.ReviewID = wr.Review.ID
	confirm.IdempotencyKey = "synthetic-warehouse"
	if _, err = db.ConfirmWarehouseReview(ctx, confirm); err != nil {
		t.Fatal(err)
	}
	a := domain.StockSyncAttestation{ProductID: testpublication.OzonProduct, WarehouseID: testpublication.OzonWarehouse, Unreflected: "0", UnseenBound: "0", SafetyStock: "0"}
	for _, code := range []string{commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofSourceOrdering, commitment.ProofUnassigned, commitment.ProofAttribution, commitment.ProofPostingCoverage, commitment.ProofCompeting, commitment.ProofBound, commitment.ProofCustomerOrders} {
		a.Proofs = append(a.Proofs, domain.StockSyncProof{Code: code, Reference: "synthetic operator test assumptions", AsOf: source.StartedAt.Add(-time.Second), ValidUntil: now.Add(5 * time.Minute)})
	}
	review, err := db.PreviewStockSync(ctx, domain.StockSyncPreviewRequest{StockSyncScope: scope, Attestations: []domain.StockSyncAttestation{a}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !review.Ready || len(review.Targets) != 1 || review.Targets[0].Quantity == nil || *review.Targets[0].Quantity != "7" {
		t.Fatalf("fixture review not ready: %+v %+v", review.Checks, review.Targets)
	}
	return db, review
}

func controlledEnable(t *testing.T, db *postgres.Store, review domain.StockSyncReview) domain.StockSyncView {
	t.Helper()
	v, err := db.MutateStockSync(context.Background(), review.StockSyncScope, "enable", review.ID, "synthetic-intent-key-001", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if v.LastOperation == nil {
		t.Fatal("publication missing")
	}
	return v
}

func TestControlledStockPublicationOutboxAndRecovery(t *testing.T) {
	t.Run("single dispatch and reconciled readback", func(t *testing.T) {
		db, review := controlledFixture(t)
		ctx := context.Background()
		v := controlledEnable(t, db, review)
		replay := controlledEnable(t, db, review)
		if replay.LastOperation.ID != v.LastOperation.ID {
			t.Fatal("duplicate submission created another operation")
		}
		var writes, reads atomic.Int32
		client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v2/products/stocks":
				writes.Add(1)
				fmt.Fprint(w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
			case "/v2/product/info/stocks-by-warehouse/fbs":
				reads.Add(1)
				fmt.Fprint(w, `{"products":[{"product_id":2764544165,"warehouse_id":1020001323428000,"offer_id":"ATEST-897677","free_stock":7,"reserved":0,"present":7}],"has_next":false,"cursor":""}`)
			default:
				t.Errorf("unexpected fake endpoint %s", r.URL.Path)
				http.Error(w, "unexpected", 500)
			}
		})
		runner := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := runner.RunNext(ctx); err != nil {
					t.Errorf("runner: %v", err)
				}
			}()
		}
		wg.Wait()
		op, err := db.StockPublication(ctx, review.StockSyncScope, v.LastOperation.ID)
		if err != nil {
			t.Fatal(err)
		}
		var state string
		var attempts int
		if err = db.Pool.QueryRow(ctx, `SELECT state,attempts FROM outbox_tasks WHERE publication_operation_id=$1`, op.ID).Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if writes.Load() != 1 || reads.Load() != 1 || attempts != 1 || state != "succeeded" || op.State != "RECONCILED" {
			t.Fatalf("writes=%d reads=%d outbox=%s/%d operation=%+v", writes.Load(), reads.Load(), state, attempts, op)
		}
		if op.Targets[0].ProviderUpdated == nil || !*op.Targets[0].ProviderUpdated || len(op.Targets[0].ProviderErrorCodes) != 0 {
			t.Fatalf("provider acknowledgement missing: %+v", op.Targets[0])
		}
	})
	t.Run("expired claim is never replayed", func(t *testing.T) {
		db, review := controlledFixture(t)
		ctx := context.Background()
		v := controlledEnable(t, db, review)
		op, err := db.NextControlledStockPublication(ctx, time.Now().UTC())
		if err != nil || op == nil {
			t.Fatalf("claim: %v", err)
		}
		if err = db.RecoverControlledStockPublications(ctx, time.Now().UTC().Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if allowed, e := db.StockPublicationCanDispatch(ctx, *op, time.Now().UTC()); e != nil || allowed {
			t.Fatalf("expired claimant accepted: %v %v", allowed, e)
		}
		if again, e := db.NextControlledStockPublication(ctx, time.Now().UTC().Add(2*time.Minute)); e != nil || again != nil {
			t.Fatalf("expired task replayed: %+v %v", again, e)
		}
		got, err := db.StockPublication(ctx, review.StockSyncScope, v.LastOperation.ID)
		if err != nil || got.State != "UNKNOWN" {
			t.Fatalf("recovered state: %+v %v", got, err)
		}
	})
}

func TestControlledStockPublicationUncertainResultsNeverRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		lost   bool
	}{
		{"forbidden", 403, "", false}, {"rate_limited", 429, "", false}, {"server_error", 500, "", false},
		{"lost_response", 0, "", true}, {"per_target_error", 200, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":false,"errors":[{"code":"PRICE_IS_NOT_SENT"},{"code":"INVALID_ARGUMENT"},{"code":"synthetic-private-key"}]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, review := controlledFixture(t)
			ctx := context.Background()
			v := controlledEnable(t, db, review)
			var writes atomic.Int32
			client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				writes.Add(1)
				if tc.lost {
					h, ok := w.(http.Hijacker)
					if !ok {
						t.Error("hijack unavailable")
						return
					}
					c, _, err := h.Hijack()
					if err == nil {
						_ = c.Close()
					}
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			runner := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
			if _, err := runner.RunNext(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.RunNext(ctx); err != nil {
				t.Fatal(err)
			}
			op, err := db.StockPublication(ctx, review.StockSyncScope, v.LastOperation.ID)
			if err != nil {
				t.Fatal(err)
			}
			var state string
			var attempts int
			if err = db.Pool.QueryRow(ctx, `SELECT state,attempts FROM outbox_tasks WHERE publication_operation_id=$1`, op.ID).Scan(&state, &attempts); err != nil {
				t.Fatal(err)
			}
			if writes.Load() != 1 || state != "failed" || attempts != 1 || op.State != "UNKNOWN" {
				t.Fatalf("writes=%d outbox=%s/%d operation=%+v", writes.Load(), state, attempts, op)
			}
			if tc.status > 0 && (op.Targets[0].HTTPStatus == nil || *op.Targets[0].HTTPStatus != tc.status) {
				t.Fatalf("HTTP status not persisted: %+v", op.Targets[0])
			}
			if tc.name == "per_target_error" {
				v := op.Targets[0]
				if v.ProviderUpdated == nil || *v.ProviderUpdated || len(v.ProviderErrorCodes) != 3 || v.ProviderErrorCodes[0] != "PRICE_IS_NOT_SENT" || v.ProviderErrorCodes[1] != "INVALID_ARGUMENT" || v.ProviderErrorCodes[2] != "MARKETPLACE_REJECTED" {
					t.Fatalf("per-target errors lost or exposed: %+v", v)
				}
				if _, err = db.Pool.Exec(ctx, `UPDATE stock_publication_targets SET provider_updated=true WHERE id=$1`, v.ID); err == nil {
					t.Fatal("provider result was mutable")
				}
				// The rollback must refuse to discard persisted provider evidence.
				rollbackEmptySaaSMigrations(t, db)
				renewalDown := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
				if _, e := renewalDown.CombinedOutput(); e != nil {
					t.Fatal("could not roll back empty initial-renewal migration")
				}
				initialDown := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
				if _, e := initialDown.CombinedOutput(); e != nil {
					t.Fatal("could not roll back empty initial-publication migration")
				}
				recoveryDown := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
				if _, e := recoveryDown.CombinedOutput(); e != nil {
					t.Fatal("could not roll back enrolled recovery migration")
				}
				cycleDown := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
				if _, e := cycleDown.CombinedOutput(); e != nil {
					t.Fatal("could not roll back empty cycle-target migration")
				}
				cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
				output, rollbackErr := cmd.CombinedOutput()
				if rollbackErr == nil || !strings.Contains(string(output), "refusing to discard provider result evidence") {
					t.Fatal("migration allowed or failed to protect the provider result")
				}
			}
		})
	}
}

func TestControlledStockPublicationReadbackMismatchIsFenced(t *testing.T) {
	db, review := controlledFixture(t)
	ctx := context.Background()
	v := controlledEnable(t, db, review)
	var writes, reads atomic.Int32
	var observed atomic.Int32
	observed.Store(3)
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/products/stocks" {
			writes.Add(1)
			fmt.Fprint(w, `{"result":[{"product_id":2764544165,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
			return
		}
		reads.Add(1)
		fmt.Fprintf(w, `{"products":[{"product_id":2764544165,"warehouse_id":1020001323428000,"offer_id":"ATEST-897677","free_stock":%d,"reserved":0,"present":%d}],"has_next":false,"cursor":""}`, observed.Load(), observed.Load())
	})
	runner := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
	if _, err := runner.RunNext(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := db.StockPublication(ctx, review.StockSyncScope, v.LastOperation.ID)
	if err != nil || op.State != "ACKNOWLEDGED" || op.Targets[0].ReadbackQuantity == nil || *op.Targets[0].ReadbackQuantity != "3" {
		t.Fatalf("mismatch state %+v %v", op, err)
	}
	if _, err = db.MutateStockSync(ctx, review.StockSyncScope, "enable", review.ID, "different-intent-key-002", time.Now().UTC()); err != domain.ErrStockSyncBlocked {
		t.Fatalf("unresolved target allowed another intent: %v", err)
	}
	observed.Store(7)
	view, err := runner.Reconcile(ctx, review.StockSyncScope, op.ID)
	if err != nil || view.LastOperation == nil || view.LastOperation.State != "RECONCILED" {
		t.Fatalf("explicit readback %+v %v", view.LastOperation, err)
	}
	if writes.Load() != 1 || reads.Load() != 2 {
		t.Fatalf("unexpected traffic writes=%d reads=%d", writes.Load(), reads.Load())
	}
	var outcomes int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_publication_readbacks WHERE target_id=$1`, op.Targets[0].ID).Scan(&outcomes); err != nil || outcomes != 2 {
		t.Fatalf("readback audit: %d %v", outcomes, err)
	}
}

func TestControlledUnknownReadbackIsAuditedWithoutResending(t *testing.T) {
	db, review := controlledFixture(t)
	ctx := context.Background()
	v := controlledEnable(t, db, review)
	var writes, reads atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/products/stocks" {
			writes.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reads.Add(1)
		fmt.Fprint(w, `{"products":[{"product_id":2764544165,"warehouse_id":1020001323428000,"offer_id":"ATEST-897677","free_stock":0,"reserved":0,"present":0}],"has_next":false,"cursor":""}`)
	})
	runner := stocksync.Runner{Enabled: true, ControlledTest: true, WorkerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
	if _, err := runner.RunNext(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := runner.Reconcile(ctx, review.StockSyncScope, v.LastOperation.ID)
	if err != nil || view.LastOperation == nil || view.LastOperation.State != "UNKNOWN" || view.LastOperation.Targets[0].ReadbackQuantity == nil || *view.LastOperation.Targets[0].ReadbackQuantity != "0" {
		t.Fatalf("unknown readback: %+v %v", view.LastOperation, err)
	}
	if _, err = runner.RunNext(ctx); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("unexpected requests: %d writes %d reads", writes.Load(), reads.Load())
	}
	var outcome string
	if err = db.Pool.QueryRow(ctx, `SELECT outcome FROM stock_publication_readbacks WHERE target_id=$1`, view.LastOperation.Targets[0].ID).Scan(&outcome); err != nil || outcome != "MISMATCH" {
		t.Fatalf("readback audit: %s %v", outcome, err)
	}
}
