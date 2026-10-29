//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stocksync"
)

type publicationFixture struct {
	db     *postgres.Store
	scope  domain.StockSyncScope
	source domain.InventoryImport
	market domain.OzonImport
	attest []domain.StockSyncAttestation
}

func publicationData(t *testing.T, two bool) publicationFixture {
	t.Helper()
	ctx := context.Background()
	url := os.Getenv("TEST_DATABASE_URL")
	if !strings.Contains(url, "127.0.0.1") || !strings.Contains(url, "styk_stock_publication_v1_test") {
		t.Skip("dedicated local stock publication test database required")
	}
	db, err := postgres.Open(ctx, url, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "pub-t-" + suffix, MoyskladAccount: "pub-m-" + suffix, OzonAccount: "pub-o-" + suffix}
	observed := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	stock, reserve, incoming := "10", "1", "0"
	rfbs := false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: observed.Add(-time.Second), FinishedAt: observed, Entities: []domain.ExternalEntity{{Kind: "variant", ID: "item", Article: "offer"}, {Kind: "store", ID: "warehouse"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "variant", ExternalID: "item", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: observed.Add(-time.Second), FinishedAt: observed, Products: []domain.OzonProduct{{ProductID: "123", OfferID: "offer", SKU: "321"}}, Warehouses: []domain.OzonWarehouse{{ID: "456", IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: "123", OfferID: "offer", SKU: "321", WarehouseID: "456", Present: 5, Reserved: 1, FreeStock: 4}}}
	if two {
		source.Entities = append(source.Entities, domain.ExternalEntity{Kind: "variant", ID: "item2", Article: "offer2"})
		source.WarehouseStock = append(source.WarehouseStock, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "item2", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming})
		market.Products = append(market.Products, domain.OzonProduct{ProductID: "124", OfferID: "offer2", SKU: "322"})
		market.Stocks = append(market.Stocks, domain.OzonStock{ProductID: "124", OfferID: "offer2", SKU: "322", WarehouseID: "456", Present: 5, Reserved: 1, FreeStock: 4})
	}
	if _, err = db.SaveInventoryImport(ctx, source, "pub-source-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveOzonImport(ctx, market, "pub-ozon-"+suffix); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	for i, p := range market.Products {
		item := "item"
		if i == 1 {
			item = "item2"
		}
		if _, err = db.ConfirmMatch(ctx, match, p.ProductID, "variant", item, "synthetic operator-confirmed product"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ConfirmWarehouseMapping(ctx, match, "warehouse", "456", "synthetic operator-confirmed warehouse"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err = db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic stock publication owner", true); err != nil {
			t.Fatal(err)
		}
	}
	var attest []domain.StockSyncAttestation
	for _, p := range market.Products {
		a := domain.StockSyncAttestation{ProductID: p.ProductID, WarehouseID: "456", Unreflected: "0", UnseenBound: "0", SafetyStock: "1"}
		for _, code := range []string{commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofSourceOrdering, commitment.ProofUnassigned, commitment.ProofAttribution, commitment.ProofPostingCoverage, commitment.ProofCompeting, commitment.ProofBound, commitment.ProofCustomerOrders} {
			a.Proofs = append(a.Proofs, domain.StockSyncProof{Code: code, Reference: "synthetic independently checked control: " + code, AsOf: observed, ValidUntil: time.Now().UTC().Add(5 * time.Minute)})
		}
		attest = append(attest, a)
	}
	t.Cleanup(func() {
		// Durable history stays intact, while a test must not leave enabled work
		// for the next test's deliberately global worker claim.
		_, err := db.MutateStockSync(context.Background(), scope, "disable", "", "fixture-stop-"+suffix, time.Now().UTC())
		if err != nil && !errors.Is(err, domain.ErrStockSyncNotFound) {
			t.Errorf("disable publication fixture: %v", err)
		}
	})
	return publicationFixture{db, scope, source, market, attest}
}
func (f publicationFixture) preview(t *testing.T, attested bool) domain.StockSyncReview {
	t.Helper()
	req := domain.StockSyncPreviewRequest{StockSyncScope: f.scope}
	if attested {
		req.Attestations = f.attest
	}
	r, err := f.db.PreviewStockSync(context.Background(), req, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f publicationFixture) enable(t *testing.T) domain.StockSyncView {
	t.Helper()
	r := f.preview(t, true)
	if !r.Ready {
		for _, target := range r.Targets {
			for _, c := range target.Checks {
				if !c.Passed {
					t.Log(c.Code, c.Message)
				}
			}
		}
		t.Fatalf("synthetic proven review is not ready: %+v", r.Checks)
	}
	view, err := f.db.MutateStockSync(context.Background(), f.scope, "enable", r.ID, "enable-"+r.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return view
}
func publicationClient(t *testing.T, h http.HandlerFunc) *ozon.Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	client, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: "synthetic-publication-seller", APIKey: "synthetic-publication-secret", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 5, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func publicationRunner(f publicationFixture, client *ozon.Client) stocksync.Runner {
	return stocksync.Runner{Store: f.db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
}
func TestStockPublicationRequiresProofsScopedReviewAndExplicitIntent(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	r := f.preview(t, false)
	if r.Ready || len(r.Targets) != 1 || r.Targets[0].Quantity != nil {
		t.Fatal("ordinary imports unexpectedly publishable")
	}
	if _, err := f.db.MutateStockSync(ctx, f.scope, "enable", r.ID, "unproved-intent-1234", time.Now()); !errors.Is(err, domain.ErrStockSyncBlocked) {
		t.Fatalf("unproved enable: %v", err)
	}
	r = f.preview(t, true)
	if !r.Ready || r.Targets[0].Quantity == nil || *r.Targets[0].Quantity != "8" {
		t.Fatalf("wrong conditional quantity: %+v", r.Targets[0])
	}
	other := f.scope
	other.Tenant = "other-tenant"
	if _, err := f.db.MutateStockSync(ctx, other, "enable", r.ID, "other-tenant-intent-1234", time.Now()); !errors.Is(err, domain.ErrStockSyncNotFound) {
		t.Fatalf("cross-scope review: %v", err)
	}
	view, err := f.db.MutateStockSync(ctx, f.scope, "enable", r.ID, "same-enable-intent-1234", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.db.MutateStockSync(ctx, f.scope, "enable", r.ID, "same-enable-intent-1234", time.Now())
	if err != nil || replay.LastOperation.ID != view.LastOperation.ID {
		t.Fatalf("idempotency failed: %v", err)
	}
	if _, err = f.db.MutateStockSync(ctx, f.scope, "disable", "", "same-enable-intent-1234", time.Now()); !errors.Is(err, domain.ErrStockSyncConflict) {
		t.Fatalf("intent changed: %v", err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE stock_publication_targets SET quantity='99' WHERE operation_id=$1`, view.LastOperation.ID); err == nil {
		t.Fatal("frozen quantity mutated")
	}
	disabled, err := f.db.MutateStockSync(ctx, f.scope, "disable", "", "disable-intent-123456", time.Now())
	if err != nil || disabled.Enabled || disabled.LastOperation.State != "CANCELLED" {
		t.Fatalf("queued disable: %+v %v", disabled, err)
	}
	if op, err := f.db.NextStockPublication(ctx, time.Now()); err != nil || op != nil {
		t.Fatalf("disabled operation dispatched: %+v %v", op, err)
	}
}
func TestStockPublicationUnknownNeverReplaysAndReadbackIsExplicit(t *testing.T) {
	f := publicationData(t, false)
	f.enable(t)
	var writes, reads atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/products/stocks":
			writes.Add(1)
			w.WriteHeader(500)
			fmt.Fprint(w, "private raw failure")
		case "/v2/product/info/stocks-by-warehouse/fbs":
			reads.Add(1)
			fmt.Fprint(w, `{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8}],"has_next":false,"cursor":""}`)
		default:
			t.Error("unexpected external operation")
			w.WriteHeader(400)
		}
	})
	runner := publicationRunner(f, client)
	worked, err := runner.RunNext(context.Background())
	if err != nil || !worked || writes.Load() != 1 {
		t.Fatalf("dispatch: %v %v %d", worked, err, writes.Load())
	}
	view, err := f.db.StockSyncStatus(context.Background(), f.scope, time.Now())
	if err != nil || view.LastOperation.State != "UNKNOWN" {
		t.Fatalf("unknown outcome not retained: %+v %v", view, err)
	}
	for i := 0; i < 3; i++ {
		if _, err = runner.RunNext(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if writes.Load() != 1 || reads.Load() != 0 {
		t.Fatal("uncertain request was automatically repeated or reconciled")
	}
	view, err = runner.Reconcile(context.Background(), f.scope, view.LastOperation.ID)
	if err != nil || view.LastOperation.State != "CONFIRMED_EFFECTIVE" || view.Enabled || view.State != "EFFECT_CONFIRMED_REVIEW_REQUIRED" || view.LastConfirmedAt != nil || writes.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("readback effect: %+v %v", view, err)
	}
	oldProof := f.preview(t, true)
	if oldProof.Ready {
		t.Fatal("old pre-send competing proof reused after unknown outcome")
	}
	oldOperation := view.LastOperation.ID
	settledAt := time.Now().UTC().Truncate(time.Microsecond)
	value := "11"
	f.source.FinishedAt = settledAt
	f.source.WarehouseStock[0].Stock = &value
	if _, err = f.db.SaveInventoryImport(context.Background(), f.source, "settled-source-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	if err = f.db.RefreshStockSyncScope(context.Background(), f.scope, settledAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if worked, err = runner.RunNext(context.Background()); err != nil || worked || writes.Load() != 1 {
		t.Fatalf("readback automatically resumed writes: %v %v", worked, err)
	}
	for i := range f.attest[0].Proofs {
		f.attest[0].Proofs[i].AsOf = settledAt
		f.attest[0].Proofs[i].ValidUntil = settledAt.Add(5 * time.Minute)
		f.attest[0].Proofs[i].Reference = "synthetic operator settlement: no outstanding writer for " + oldOperation
	}
	fresh := f.preview(t, true)
	if !fresh.Ready {
		t.Fatal("fresh scoped settlement and source observation rejected")
	}
	resumed, err := f.db.MutateStockSync(context.Background(), f.scope, "enable", fresh.ID, "explicit-settlement-resume-1234", time.Now())
	if err != nil || !resumed.Enabled || resumed.LastOperation.ID == oldOperation {
		t.Fatalf("explicit safe resume failed: %+v %v", resumed, err)
	}
}
func TestStockPublicationPartialResultAndContinuousChangedEvidence(t *testing.T) {
	f := publicationData(t, true)
	f.enable(t)
	var writes atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writes.Add(1)
		fmt.Fprint(w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":124,"warehouse_id":456,"updated":false,"errors":[{"code":"PRICE_IS_NOT_SENT"}]}]}`)
	})
	runner := publicationRunner(f, client)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := f.db.StockSyncStatus(context.Background(), f.scope, time.Now())
	if err != nil || view.LastOperation.State != "PARTIAL" || len(view.LastOperation.Targets) != 2 || writes.Load() != 1 {
		t.Fatalf("partial result lost: %+v %v", view, err)
	}
	if view.LastOperation.Targets[0].State != "SUCCEEDED" || view.LastOperation.Targets[1].State != "FAILED" {
		t.Fatalf("per-target result: %+v", view.LastOperation.Targets)
	}
	// A fresh source import with changed quantities can enqueue only one new
	// bounded job under the still-valid, unchanged scoped controls.
	now := time.Now().UTC()
	value := "12"
	f.source.FinishedAt = now
	for i := range f.source.WarehouseStock {
		f.source.WarehouseStock[i].Stock = &value
	}
	if _, err = f.db.SaveInventoryImport(context.Background(), f.source, "changed-source-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	if err = f.db.RefreshStockSyncScope(context.Background(), f.scope, now.Add(31*time.Second)); err != nil {
		t.Fatal(err)
	}
	op, err := f.db.NextStockPublication(context.Background(), now.Add(31*time.Second))
	if err != nil || op == nil || len(op.Targets) != 2 || op.Targets[0].Quantity != "10" {
		t.Fatalf("changed evidence not scheduled: %+v %v", op, err)
	}
	for _, target := range op.Targets {
		if err = f.db.FinishStockTarget(context.Background(), f.scope, op.ID, target.ID, "SUCCEEDED", "", now.Add(31*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.db.RefreshStockSyncScope(context.Background(), f.scope, now.Add(38*time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM stock_publications WHERE tenant_id=$1`, f.scope.Tenant).Scan(&count); err != nil || count != 2 {
		t.Fatalf("identical job loop: %d %v", count, err)
	}
}

func TestStockPublicationPartialReadbackKeepsUnknownTargetsVisible(t *testing.T) {
	f := publicationData(t, true)
	f.enable(t)
	var writes atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/products/stocks" {
			writes.Add(1)
			w.WriteHeader(500)
			return
		}
		if r.URL.Path != "/v2/product/info/stocks-by-warehouse/fbs" {
			t.Error("unexpected external operation")
			w.WriteHeader(400)
			return
		}
		var request struct {
			Offers []string `json:"offer_id"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Offers) != 1 {
			t.Error("invalid bounded readback request")
			w.WriteHeader(400)
			return
		}
		if request.Offers[0] == "offer" {
			fmt.Fprint(w, `{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8}],"has_next":false,"cursor":""}`)
		} else {
			fmt.Fprint(w, `{"products":[{"product_id":124,"warehouse_id":456,"offer_id":"offer2","free_stock":7}],"has_next":false,"cursor":""}`)
		}
	})
	runner := publicationRunner(f, client)
	ctx := context.Background()
	if _, err := runner.RunNext(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := f.db.StockSyncStatus(ctx, f.scope, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	after, err := runner.Reconcile(ctx, f.scope, before.LastOperation.ID)
	if err != nil || after.Enabled || after.State != "DISABLED_OUTCOME_UNKNOWN" || after.LastOperation.State != "UNKNOWN" || after.LastConfirmedAt != nil {
		t.Fatalf("partial readback hid unresolved target: state=%s err=%v", after.State, err)
	}
	if after.LastOperation.Targets[0].State != "CONFIRMED_EFFECTIVE" || after.LastOperation.Targets[1].State != "UNKNOWN" {
		t.Fatal("partial readback per-target outcomes lost")
	}
	if worked, err := runner.RunNext(ctx); err != nil || worked || writes.Load() != 1 {
		t.Fatalf("partial readback automatically resumed: %v %v", worked, err)
	}
	if _, err = f.db.MutateStockSync(ctx, f.scope, "enable", f.preview(t, true).ID, "partial-readback-enable-1234", time.Now().UTC()); !errors.Is(err, domain.ErrStockSyncBlocked) {
		t.Fatalf("partial unknown allowed enable: %v", err)
	}
}
func TestStockPublicationStaleReviewAndExpiredProofsFailClosed(t *testing.T) {
	f := publicationData(t, false)
	r := f.preview(t, true)
	now := time.Now().UTC()
	value := "11"
	f.source.FinishedAt = now
	f.source.WarehouseStock[0].Stock = &value
	if _, err := f.db.SaveInventoryImport(context.Background(), f.source, "new-source-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.MutateStockSync(context.Background(), f.scope, "enable", r.ID, "stale-enable-intent-1234", now); !errors.Is(err, domain.ErrStockSyncStale) {
		t.Fatalf("stale source dispatched: %v", err)
	}
	f.enable(t)
	view, err := f.db.StockSyncStatus(context.Background(), f.scope, now.Add(16*time.Minute))
	if err != nil || view.Ready || view.State == "ACTIVE" {
		t.Fatalf("expired proof stayed active: %+v %v", view, err)
	}
	if op, err := f.db.NextStockPublication(context.Background(), now.Add(16*time.Minute)); err != nil || op != nil {
		t.Fatalf("expired proof dispatched: %+v %v", op, err)
	}
}

func TestStockPublicationConcurrentIntentAndDispatchAreSingle(t *testing.T) {
	f := publicationData(t, false)
	review := f.preview(t, true)
	ctx := context.Background()
	const count = 6
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() {
			_, err := f.db.MutateStockSync(ctx, f.scope, "enable", review.ID, "concurrent-enable-intent-1234", time.Now())
			results <- err
		}()
	}
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var operations int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_publications WHERE tenant_id=$1`, f.scope.Tenant).Scan(&operations); err != nil || operations != 1 {
		t.Fatalf("duplicate operations: %d %v", operations, err)
	}
	claims := make(chan *domain.StockPublication, count)
	for i := 0; i < count; i++ {
		go func() {
			op, err := f.db.NextStockPublication(ctx, time.Now())
			if err != nil {
				results <- err
			} else {
				results <- nil
			}
			claims <- op
		}()
	}
	claimed := 0
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
		if <-claims != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("single write fence claimed %d times", claimed)
	}
	view, err := f.db.MutateStockSync(ctx, f.scope, "disable", "", "concurrent-disable-intent-1234", time.Now())
	if err != nil || view.State != "STOPPING" {
		t.Fatalf("in-flight disable falsely completed: %s %v", view.State, err)
	}
}

func TestStockPublicationTargetFenceSpansMoyskladScopes(t *testing.T) {
	f := publicationData(t, false)
	view := f.enable(t)
	ctx := context.Background()
	first := view.LastOperation
	other := f.scope
	other.MoyskladAccount += "-other"
	if err := f.db.RegisterStockConnection(ctx, other.Tenant, "moysklad", other.MoyskladAccount, "synthetic second source owner", false); err != nil {
		t.Fatal(err)
	}
	// Isolate the durable fence from the stricter mapping preconditions by
	// creating a second scoped intent directly. Even a future application bug
	// must not create two pending writes for one physical marketplace target.
	reviewID, operationID := first.ID, first.ReviewID
	_, err := f.db.Pool.Exec(ctx, `INSERT INTO stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account,ready,snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,false,'{}',now(),now()+interval '1 minute')`, reviewID, other.Tenant, other.MoyskladAccount, other.OzonAccount)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO stock_sync_settings(tenant_id,moysklad_account,ozon_account,enabled,review_id) VALUES($1,$2,$3,false,$4)`, other.Tenant, other.MoyskladAccount, other.OzonAccount, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO stock_publications(id,tenant_id,moysklad_account,ozon_account,review_id) VALUES($1,$2,$3,$4,$5)`, operationID, other.Tenant, other.MoyskladAccount, other.OzonAccount, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(first.Targets[0].Decision)
	if err != nil {
		t.Fatal(err)
	}
	fenced := func(state string) {
		t.Helper()
		_, err := f.db.Pool.Exec(ctx, `INSERT INTO stock_publication_targets(operation_id,ozon_account,product_id,warehouse_id,quantity,state,decision) VALUES($1,$2,'123','456','8','QUEUED',$3::jsonb)`, operationID, other.OzonAccount, raw)
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || conflict.Code != "23505" || conflict.ConstraintName != "stock_publication_target_fence" {
			t.Fatalf("cross-source target not fenced in %s: %v", state, err)
		}
	}
	fenced("QUEUED")
	op, err := f.db.NextStockPublication(ctx, time.Now().UTC())
	if err != nil || op == nil || op.ID != first.ID {
		t.Fatalf("claim scoped original: %v", err)
	}
	fenced("DISPATCHING")
	if err = f.db.FinishStockTarget(ctx, f.scope, first.ID, first.Targets[0].ID, "UNKNOWN", "SYNTHETIC_UNKNOWN", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fenced("UNKNOWN")
}

func TestStockPublicationCredentialRevisionRequiresNewReview(t *testing.T) {
	f := publicationData(t, false)
	view := f.enable(t)
	ctx := context.Background()
	_, _, err := f.db.SaveCredential(ctx, credentials.Scope{Tenant: f.scope.Tenant, Provider: "ozon", Account: f.scope.OzonAccount}, "Synthetic seller", 0, "synthetic-credential-save", make([]byte, 32), make([]byte, 29), time.Now().UTC(), "synthetic scoped account check")
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.db.StockSyncStatus(ctx, f.scope, time.Now().UTC())
	if err != nil || status.Ready || status.State == "ACTIVE" {
		t.Fatalf("changed credential remained ready: %s %v", status.State, err)
	}
	if op, err := f.db.NextStockPublication(ctx, time.Now().UTC()); err != nil || op != nil {
		t.Fatalf("frozen credential revision dispatched: %v", err)
	}
	if _, err = f.db.MutateStockSync(ctx, f.scope, "enable", view.LastOperation.ReviewID, "old-review-renewal-1234", time.Now().UTC()); !errors.Is(err, domain.ErrStockSyncBlocked) {
		t.Fatalf("old review renewed with a new key: %v", err)
	}
	refreshed := f.preview(t, true)
	renewed, err := f.db.MutateStockSync(ctx, f.scope, "enable", refreshed.ID, "fresh-review-renewal-1234", time.Now().UTC())
	if err != nil || !renewed.Enabled || renewed.LastOperation.ID == view.LastOperation.ID {
		t.Fatalf("new explicit review could not renew: %v", err)
	}
}

func TestStockPublicationHTTPExplicitEnableReplayAndDisable(t *testing.T) {
	f := publicationData(t, false)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("STYK_REVIEW_LIFETIME", "")
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "http://" + server.Listener.Addr().String()
	secret := strings.Repeat("s", 32)
	handler, err := console.Handler(f.db, secret, origin)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	client.Timeout = 5 * time.Second
	call := func(method, path string, body any, expected int) []byte {
		t.Helper()
		var payload []byte
		if body != nil {
			var err error
			payload, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, origin+path, strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != expected {
			t.Fatalf("%s %s: status %d, expected %d; response %s", method, path, response.StatusCode, expected, raw)
		}
		for _, hidden := range []string{`"decision":`, `"attestations":`, `"ozon_credential_revision":`, `"moysklad_credential_revision":`} {
			if strings.Contains(string(raw), hidden) {
				t.Fatalf("%s exposed an internal frozen evidence field", path)
			}
		}
		return raw
	}
	call("POST", "/api/session", map[string]string{"secret": secret}, http.StatusNoContent)
	previewBody := domain.StockSyncPreviewRequest{StockSyncScope: f.scope, Attestations: f.attest}
	var review domain.StockSyncReview
	if err = json.Unmarshal(call("POST", "/api/stock-sync/preview", previewBody, http.StatusOK), &review); err != nil || !review.Ready || len(review.ID) != 32 {
		t.Fatalf("HTTP preview not ready: %v", err)
	}
	type mutation struct {
		domain.StockSyncScope
		ReviewID    string `json:"review_id,omitempty"`
		Key         string `json:"idempotency_key"`
		Acknowledge bool   `json:"acknowledge"`
	}
	enable := mutation{f.scope, review.ID, "http-explicit-enable-1234", true}
	var enabled domain.StockSyncView
	if err = json.Unmarshal(call("POST", "/api/stock-sync/enable", enable, http.StatusOK), &enabled); err != nil || !enabled.Enabled || enabled.State != "QUEUED" || enabled.LastOperation == nil {
		t.Fatalf("HTTP enable not queued: %v", err)
	}
	query := url.Values{"tenant": {f.scope.Tenant}, "moysklad_account": {f.scope.MoyskladAccount}, "ozon_account": {f.scope.OzonAccount}}
	var current domain.StockSyncView
	if err = json.Unmarshal(call("GET", "/api/stock-sync?"+query.Encode(), nil, http.StatusOK), &current); err != nil || current.State != "QUEUED" || current.LastOperation == nil || current.LastOperation.ID != enabled.LastOperation.ID {
		t.Fatalf("HTTP queued status lost: %v", err)
	}
	var replay domain.StockSyncView
	if err = json.Unmarshal(call("POST", "/api/stock-sync/enable", enable, http.StatusOK), &replay); err != nil || replay.LastOperation == nil || replay.LastOperation.ID != enabled.LastOperation.ID {
		t.Fatalf("HTTP identical replay created a different operation: %v", err)
	}
	conflict := mutation{StockSyncScope: f.scope, Key: enable.Key, Acknowledge: true}
	var conflictBody struct {
		Error string `json:"error"`
	}
	if err = json.Unmarshal(call("POST", "/api/stock-sync/disable", conflict, http.StatusConflict), &conflictBody); err != nil || conflictBody.Error != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("HTTP changed intent did not conflict: %v", err)
	}
	var count int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM stock_publications WHERE tenant_id=$1`, f.scope.Tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("HTTP retry duplicated durable work: %d %v", count, err)
	}
	disable := mutation{StockSyncScope: f.scope, Key: "http-explicit-disable-1234", Acknowledge: true}
	var disabled domain.StockSyncView
	if err = json.Unmarshal(call("POST", "/api/stock-sync/disable", disable, http.StatusOK), &disabled); err != nil || disabled.Enabled || disabled.LastOperation == nil || disabled.LastOperation.ID != enabled.LastOperation.ID || disabled.LastOperation.State != "CANCELLED" {
		t.Fatalf("HTTP queued disable failed: %v", err)
	}
	if disabled.LastOperation.Targets[0].DispatchedAt != nil {
		t.Fatal("HTTP-only queued workflow dispatched an external write")
	}
}
