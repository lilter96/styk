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
	"os/exec"
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
	// Synthetic assertions are accepted only by the explicit loopback simulator.
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "http://127.0.0.1:1")
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
	archived := false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: observed.Add(-time.Second), FinishedAt: observed, Entities: []domain.ExternalEntity{{Kind: "variant", ID: "item", Article: "offer"}, {Kind: "store", ID: "warehouse"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "variant", ExternalID: "item", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: observed.Add(-time.Second), FinishedAt: observed, Products: []domain.OzonProduct{{ProductID: "123", OfferID: "offer", SKU: "321", Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: "456", IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: "123", OfferID: "offer", SKU: "321", WarehouseID: "456", Present: 5, Reserved: 1, FreeStock: 4}}}
	if two {
		source.Entities = append(source.Entities, domain.ExternalEntity{Kind: "variant", ID: "item2", Article: "offer2"})
		source.WarehouseStock = append(source.WarehouseStock, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "item2", WarehouseID: "warehouse", Stock: &stock, Reserve: &reserve, InTransit: &incoming})
		market.Products = append(market.Products, domain.OzonProduct{ProductID: "124", OfferID: "offer2", SKU: "322", Archived: &archived})
		market.Stocks = append(market.Stocks, domain.OzonStock{ProductID: "124", OfferID: "offer2", SKU: "322", WarehouseID: "456", Present: 5, Reserved: 1, FreeStock: 4})
	}
	if _, err = db.SaveInventoryImport(ctx, source, "pub-source-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveOzonImport(ctx, market, "pub-ozon-"+suffix); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err = db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic stock publication owner", true); err != nil {
			t.Fatal(err)
		}
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
	var attest []domain.StockSyncAttestation
	for _, p := range market.Products {
		a := domain.StockSyncAttestation{ProductID: p.ProductID, WarehouseID: "456", Unreflected: "0", UnseenBound: "0", SafetyStock: "1"}
		for _, code := range []string{commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofSourceOrdering, commitment.ProofUnassigned, commitment.ProofAttribution, commitment.ProofPostingCoverage, commitment.ProofCompeting, commitment.ProofBound, commitment.ProofCustomerOrders} {
			a.Proofs = append(a.Proofs, domain.StockSyncProof{Code: code, Reference: "synthetic independently checked control: " + code, AsOf: source.StartedAt, ValidUntil: time.Now().UTC().Add(5 * time.Minute)})
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

func TestStockPublicationOperatorAssertionsAreNeverProviderProof(t *testing.T) {
	f := publicationData(t, false)
	review := f.preview(t, true)
	if !review.Ready {
		t.Fatal("loopback simulation did not accept synthetic assumptions")
	}
	for _, proof := range review.Targets[0].Decision.Evidence.Proofs {
		if proof.EvidenceClass == "OPERATOR_ATTESTED" && proof.State != domain.ProofAttested {
			t.Fatal("operator assertion was misclassified as independently verified")
		}
	}
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "")
	withoutSimulator := f.preview(t, true)
	if withoutSimulator.Ready || withoutSimulator.Targets[0].Quantity != nil {
		t.Fatal("ordinary customer policy accepted test assumptions")
	}
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
	client, err := ozon.New(ozon.Options{AllowSyntheticStockWrites: true, BaseURL: server.URL, ClientID: "synthetic-publication-seller", APIKey: "synthetic-publication-secret", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 5, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func publicationRunner(f publicationFixture, client *ozon.Client) stocksync.Runner {
	return stocksync.Runner{Enabled: true, Store: f.db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
}

func TestStockPublicationReviewExpiresWhenMarketplaceTraversalAgesOut(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	started := time.Now().UTC().Truncate(time.Microsecond).Add(-14*time.Minute - 30*time.Second)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE source_imports SET started_at=$1 WHERE source='ozon' AND account_id=$2`, started, f.scope.OzonAccount); err != nil {
		t.Fatal(err)
	}
	review := f.preview(t, true)
	if !review.Ready || !review.ExpiresAt.Equal(started.Add(15*time.Minute)) {
		t.Fatalf("preview did not expire with the marketplace traversal: ready=%v expiry=%v", review.Ready, review.ExpiresAt)
	}
	if _, err := f.db.MutateStockSync(ctx, f.scope, "enable", review.ID, "aged-traversal-intent-1234", review.ExpiresAt); !errors.Is(err, domain.ErrStockSyncStale) {
		t.Fatalf("expired preview was enabled: %v", err)
	}
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
			fixtureText(t, w, "private raw failure")
		case "/v2/product/info/stocks-by-warehouse/fbs":
			reads.Add(1)
			fixtureText(t, w, `{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8}],"has_next":false,"cursor":""}`)
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
	f.source.StartedAt = settledAt.Add(time.Microsecond)
	f.source.FinishedAt = settledAt.Add(2 * time.Microsecond)
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
func TestStockPublicationSuccessfulResultAndContinuousChangedEvidence(t *testing.T) {
	f := publicationData(t, true)
	f.enable(t)
	var writes atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writes.Add(1)
		fixtureText(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":124,"warehouse_id":456,"updated":true,"errors":[]}]}`)
	})
	runner := publicationRunner(f, client)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := f.db.StockSyncStatus(context.Background(), f.scope, time.Now())
	if err != nil || view.LastOperation.State != "SUCCEEDED" || len(view.LastOperation.Targets) != 2 || writes.Load() != 1 {
		t.Fatalf("partial result lost: %+v %v", view, err)
	}
	if view.LastOperation.Targets[0].State != "SUCCEEDED" || view.LastOperation.Targets[1].State != "SUCCEEDED" {
		t.Fatalf("per-target result: %+v", view.LastOperation.Targets)
	}
	// A fresh source import with changed quantities can enqueue only one new
	// bounded job under the still-valid, unchanged scoped controls.
	now := time.Now().UTC()
	value := "12"
	f.source.StartedAt = now.Add(-time.Millisecond)
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
			fixtureText(t, w, `{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8}],"has_next":false,"cursor":""}`)
		} else {
			fixtureText(t, w, `{"products":[{"product_id":124,"warehouse_id":456,"offer_id":"offer2","free_stock":7}],"has_next":false,"cursor":""}`)
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
	f.source.StartedAt = now.Add(-time.Millisecond)
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
		if !errors.As(err, &conflict) || conflict.Code != "23505" || (conflict.ConstraintName != "stock_publication_target_fence" && conflict.ConstraintName != "stock_publication_target_fences_pkey") {
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
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "http://127.0.0.1:1")
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
		defer closeFixture(t, response.Body)
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
	var beforeCount, afterCount int
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM stock_sync_reviews WHERE tenant_id=$1`, f.scope.Tenant).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	query := url.Values{"tenant": {f.scope.Tenant}, "moysklad_account": {f.scope.MoyskladAccount}, "ozon_account": {f.scope.OzonAccount}}
	var blockedShadow struct {
		Targets []struct {
			Candidate *string `json:"candidate"`
		} `json:"targets"`
	}
	if err = json.Unmarshal(call("GET", "/api/stock-sync/shadow?"+query.Encode(), nil, http.StatusOK), &blockedShadow); err != nil || len(blockedShadow.Targets) != 1 || blockedShadow.Targets[0].Candidate != nil {
		t.Fatalf("shadow invented a candidate without the required evidence: %+v %v", blockedShadow, err)
	}
	if err = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM stock_sync_reviews WHERE tenant_id=$1`, f.scope.Tenant).Scan(&afterCount); err != nil || afterCount != beforeCount {
		t.Fatalf("shadow persisted a review: before=%d after=%d err=%v", beforeCount, afterCount, err)
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
	shadowPayload := call("GET", "/api/stock-sync/shadow?"+query.Encode(), nil, http.StatusOK)
	var shadow struct {
		PublicationAvailable bool `json:"publication_available"`
		Targets              []struct {
			Physical        *string `json:"physical"`
			Reserve         *string `json:"reserve"`
			Candidate       *string `json:"candidate"`
			MarketplaceFree *string `json:"marketplace_free"`
		} `json:"targets"`
	}
	if err = json.Unmarshal(shadowPayload, &shadow); err != nil || shadow.PublicationAvailable || len(shadow.Targets) != 1 || shadow.Targets[0].Physical == nil || *shadow.Targets[0].Physical != "10" || shadow.Targets[0].Reserve == nil || *shadow.Targets[0].Reserve != "1" || shadow.Targets[0].Candidate == nil || *shadow.Targets[0].Candidate != "8" || shadow.Targets[0].MarketplaceFree == nil || *shadow.Targets[0].MarketplaceFree != "4" {
		t.Fatalf("shadow did not project the existing calculation: %s (%v)", shadowPayload, err)
	}
	otherQuery := url.Values{"tenant": {"wrong-tenant"}, "moysklad_account": {f.scope.MoyskladAccount}, "ozon_account": {f.scope.OzonAccount}}
	call("GET", "/api/stock-sync/shadow?"+otherQuery.Encode(), nil, http.StatusNotFound)
	var current domain.StockSyncView
	statusPayload := call("GET", "/api/stock-sync?"+query.Encode(), nil, http.StatusOK)
	if !strings.Contains(string(statusPayload), `"publication_available":false`) {
		t.Fatal("UI was not told that real publication is unavailable")
	}
	if err = json.Unmarshal(statusPayload, &current); err != nil || current.State != "QUEUED" || current.LastOperation == nil || current.LastOperation.ID != enabled.LastOperation.ID {
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

func TestStockPublicationRejectsRecoveredStaleWorkerClaim(t *testing.T) {
	f := publicationData(t, false)
	f.enable(t)
	ctx := context.Background()
	now := time.Now().UTC()
	op, err := f.db.NextStockPublication(ctx, now)
	if err != nil || op == nil {
		t.Fatalf("claim: %v", err)
	}
	valid, err := f.db.StockPublicationCanDispatch(ctx, *op, now)
	if err != nil || !valid {
		t.Fatalf("fresh claim invalid: %v %v", valid, err)
	}
	if err = f.db.MarkStockPublicationUnknown(ctx, f.scope, op.ID, now.Add(61*time.Second)); err != nil {
		t.Fatal(err)
	}
	valid, err = f.db.StockPublicationCanDispatch(ctx, *op, now)
	if err != nil || valid {
		t.Fatalf("recovered old claim still allowed: %v %v", valid, err)
	}
	var state string
	if err = f.db.Pool.QueryRow(ctx, `SELECT state FROM stock_publication_targets WHERE operation_id=$1`, op.ID).Scan(&state); err != nil || state != "UNKNOWN" {
		t.Fatalf("state=%q error=%v", state, err)
	}
}

func TestStockPublicationRejectsOutOfOrderImports(t *testing.T) {
	for _, provider := range []string{"moysklad", "ozon"} {
		t.Run(provider, func(t *testing.T) {
			f := publicationData(t, false)
			ctx := context.Background()
			// Original finishes before the late insert. The late traversal started
			// earlier, so it cannot become the authoritative latest stock baseline.
			if provider == "moysklad" {
				old := f.source
				old.StartedAt = f.source.StartedAt.Add(-time.Minute)
				old.FinishedAt = time.Now().UTC().Add(-time.Second)
				n := "100"
				old.WarehouseStock[0].Stock = &n
				if _, err := f.db.SaveInventoryImport(ctx, old, "out-of-order-source-"+f.scope.Tenant); err != nil {
					t.Fatal(err)
				}
			} else {
				old := f.market
				old.StartedAt = f.market.StartedAt.Add(-time.Minute)
				old.FinishedAt = time.Now().UTC().Add(-time.Second)
				old.Stocks[0].FreeStock = 99
				if _, err := f.db.SaveOzonImport(ctx, old, "out-of-order-market-"+f.scope.Tenant); err != nil {
					t.Fatal(err)
				}
			}
			review := f.preview(t, true)
			if review.Ready {
				t.Fatal("out-of-order snapshot accepted")
			}
			found := false
			for _, c := range review.Checks {
				if c.Code == "OUT_OF_ORDER_IMPORT" && !c.Passed {
					found = true
				}
			}
			if !found {
				t.Fatalf("ordering failure missing: %+v", review.Checks)
			}
			if _, err := f.db.MutateStockSync(ctx, f.scope, "enable", review.ID, "out-of-order-enable-12345", time.Now()); !errors.Is(err, domain.ErrStockSyncBlocked) {
				t.Fatalf("enable should be blocked: %v", err)
			}
		})
	}
}

func TestStockPublicationRequiresCurrentCatalogIdentity(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"offer changed", "CATALOG_IDENTITY_CHANGED"}, {"stock sku differs", "CATALOG_IDENTITY_CHANGED"}, {"sku changed consistently", "CATALOG_IDENTITY_CHANGED"}, {"offer archived", "CATALOG_IDENTITY_CHANGED"},
		{"warehouse becomes rfbs", "WAREHOUSE_SCHEMA_UNCONFIRMED"}, {"source item missing", "SOURCE_IDENTITY_UNCONFIRMED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := publicationData(t, false)
			ctx := context.Background()
			now := time.Now().UTC()
			if tc.name == "source item missing" {
				snap := f.source
				snap.StartedAt = now.Add(-10 * time.Second)
				snap.FinishedAt = now.Add(-time.Second)
				snap.Entities = []domain.ExternalEntity{{Kind: "store", ID: "warehouse"}}
				if _, err := f.db.SaveInventoryImport(ctx, snap, "identity-source-"+f.scope.Tenant); err != nil {
					t.Fatal(err)
				}
			} else {
				snap := f.market
				snap.StartedAt = now.Add(-10 * time.Second)
				snap.FinishedAt = now.Add(-time.Second)
				switch tc.name {
				case "offer changed":
					snap.Products[0].OfferID = "different-offer"
					snap.Stocks[0].OfferID = "different-offer"
				case "stock sku differs":
					snap.Stocks[0].SKU = "999"
				case "sku changed consistently":
					snap.Products[0].SKU = "999"
					snap.Stocks[0].SKU = "999"
				case "offer archived":
					yes := true
					snap.Products[0].Archived = &yes
				case "warehouse becomes rfbs":
					yes := true
					snap.Warehouses[0].IsRFBS = &yes
				}
				if _, err := f.db.SaveOzonImport(ctx, snap, "identity-market-"+f.scope.Tenant); err != nil {
					t.Fatal(err)
				}
			}
			review := f.preview(t, true)
			if review.Ready {
				t.Fatal("changed catalog identity allowed publication")
			}
			found := false
			for _, target := range review.Targets {
				for _, check := range target.Checks {
					if check.Code == tc.code && !check.Passed {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("missing failed identity check %s", tc.code)
			}
			if _, err := f.db.MutateStockSync(ctx, f.scope, "enable", review.ID, "changed-identity-enable-1234", now); !errors.Is(err, domain.ErrStockSyncBlocked) {
				t.Fatalf("unsafe enable: %v", err)
			}
		})
	}
}

func TestStockPublicationBlocksProductAndVariantSharedPool(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	source := f.source
	source.StartedAt = now.Add(-10 * time.Second)
	source.FinishedAt = now.Add(-time.Second)
	source.Entities = []domain.ExternalEntity{{Kind: "product", ID: "parent", Article: "parent-offer"}, {Kind: "variant", ID: "item", ParentID: "parent", Article: "offer"}, {Kind: "store", ID: "warehouse"}}
	productStock := source.WarehouseStock[0]
	productStock.Kind = "product"
	productStock.ExternalID = "parent"
	source.WarehouseStock = append(source.WarehouseStock, productStock)
	if _, err := f.db.SaveInventoryImport(ctx, source, "synthetic-product-and-variant-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	market := f.market
	market.StartedAt = now.Add(-10 * time.Second)
	market.FinishedAt = now.Add(-time.Second)
	notArchived := false
	market.Products = append(market.Products, domain.OzonProduct{ProductID: "124", OfferID: "parent-offer", SKU: "322", Archived: &notArchived})
	market.Stocks = append(market.Stocks, domain.OzonStock{ProductID: "124", OfferID: "parent-offer", SKU: "322", WarehouseID: "456", Present: 5, Reserved: 1, FreeStock: 4})
	if _, err := f.db.SaveOzonImport(ctx, market, "synthetic-parent-offer-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	scope := postgres.MatchScope{Tenant: f.scope.Tenant, MoyskladAccount: f.scope.MoyskladAccount, OzonAccount: f.scope.OzonAccount}
	if _, err := f.db.ConfirmMatch(ctx, scope, "124", "product", "parent", "synthetic operator parent mapping"); err != nil {
		t.Fatal(err)
	}
	review := f.preview(t, true)
	if review.Ready {
		t.Fatal("product and variant exposed the same physical stock twice")
	}
	failed := 0
	for _, target := range review.Targets {
		for _, check := range target.Checks {
			if check.Code == "SHARED_POOL_UNSUPPORTED" && !check.Passed {
				failed++
			}
		}
	}
	if failed != 2 {
		t.Fatalf("expected both parent and variant blocked, got %d", failed)
	}
}

func TestStockPublicationPartialErrorKeepsUncertainTargetFenced(t *testing.T) {
	f := publicationData(t, true)
	f.enable(t)
	var writes atomic.Int32
	c := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		fixtureText(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":124,"warehouse_id":456,"updated":false,"errors":[{"code":"INTERNAL_ERROR"}]}]}`)
	})
	runner := publicationRunner(f, c)
	if _, err := runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := f.db.StockSyncStatus(context.Background(), f.scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if view.LastOperation.State != "UNKNOWN" || view.LastOperation.Targets[0].State != "SUCCEEDED" || view.LastOperation.Targets[1].State != "UNKNOWN" {
		t.Fatalf("partial result not fenced: %+v", view.LastOperation)
	}
	for i := 0; i < 2; i++ {
		if _, err = runner.RunNext(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if writes.Load() != 1 {
		t.Fatal("uncertain target was automatically repeated")
	}
}

func TestStockPublicationLostResponseNeverReissuesRegardlessOfEffect(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(fmt.Sprintf("applied_%v", applied), func(t *testing.T) {
			f := publicationData(t, false)
			f.enable(t)
			var writes atomic.Int32
			var observed atomic.Bool
			c := publicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/products/stocks" {
					t.Error("unexpected network operation")
					return
				}
				writes.Add(1)
				observed.Store(applied)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			})
			runner := publicationRunner(f, c)
			if _, err := runner.RunNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			view, err := f.db.StockSyncStatus(context.Background(), f.scope, time.Now())
			if err != nil || view.LastOperation.State != "UNKNOWN" {
				t.Fatalf("lost response misclassified: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err = runner.RunNext(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if writes.Load() != 1 || observed.Load() != applied {
				t.Fatalf("uncertain write repeated: %d", writes.Load())
			}
		})
	}
}

func TestStockPublicationDatabaseFailureAfterDispatchDoesNotReplay(t *testing.T) {
	f := publicationData(t, false)
	f.enable(t)
	ctx := context.Background()
	workerDB, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var writes atomic.Int32
	c := publicationClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writes.Add(1)
		close(started)
		<-release
		fixtureText(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`)
	})
	runner := publicationRunner(f, c)
	runner.Store = workerDB
	done := make(chan error, 1)
	go func() { _, err := runner.RunNext(ctx); done <- err }()
	<-started
	workerDB.Pool.Close()
	close(release)
	if err = <-done; err == nil {
		t.Fatal("closed database accepted terminal write")
	}
	view, err := f.db.StockSyncStatus(ctx, f.scope, time.Now())
	if err != nil || view.LastOperation.State != "DISPATCHING" {
		t.Fatalf("dispatch fence lost: %v", err)
	}
	if err = f.db.MarkStockPublicationUnknown(ctx, f.scope, view.LastOperation.ID, time.Now().Add(61*time.Second)); err != nil {
		t.Fatal(err)
	}
	runner.Store = f.db
	if _, err = runner.RunNext(ctx); err != nil {
		t.Fatal(err)
	}
	view, err = f.db.StockSyncStatus(ctx, f.scope, time.Now())
	if err != nil || view.LastOperation.State != "UNKNOWN" || writes.Load() != 1 {
		t.Fatalf("ambiguous database failure replayed: %v", err)
	}
}

func TestStockPublicationZeroAndMissingQuantitiesAreDistinct(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	snap := f.source
	snap.StartedAt = now.Add(-10 * time.Second)
	snap.FinishedAt = now.Add(-time.Second)
	zero := "0"
	snap.WarehouseStock[0].Stock = &zero
	snap.WarehouseStock[0].Reserve = &zero
	if _, err := f.db.SaveInventoryImport(ctx, snap, "synthetic-zero-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	f.attest[0].SafetyStock = "0"
	review := f.preview(t, true)
	if !review.Ready || review.Targets[0].Quantity == nil || *review.Targets[0].Quantity != "0" {
		t.Fatal("explicit zero was not preserved")
	}
	f.attest[0].Unreflected = ""
	review = f.preview(t, true)
	if review.Ready || review.Targets[0].Quantity != nil {
		t.Fatal("missing unreflected obligation was silently treated as zero")
	}
}
func TestStockPublicationRejectsInvalidMarketplaceNumbers(t *testing.T) {
	for _, field := range []string{"present", "reserved", "free"} {
		t.Run(field, func(t *testing.T) {
			f := publicationData(t, false)
			ctx := context.Background()
			now := time.Now().UTC()
			snap := f.market
			snap.StartedAt = now.Add(-10 * time.Second)
			snap.FinishedAt = now.Add(-time.Second)
			switch field {
			case "present":
				snap.Stocks[0].Present = -1
			case "reserved":
				snap.Stocks[0].Reserved = -1
			case "free":
				snap.Stocks[0].FreeStock = -1
			}
			if _, err := f.db.SaveOzonImport(ctx, snap, "synthetic-negative-"+f.scope.Tenant); err != nil {
				t.Fatal(err)
			}
			review := f.preview(t, true)
			if review.Ready || review.Targets[0].Quantity != nil {
				t.Fatal("invalid marketplace quantities accepted")
			}
		})
	}
}
func TestStockPublicationReusedBatchDoesNotRefreshObservationTime(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	snap := f.source
	snap.StartedAt = time.Now().UTC().Add(-2 * time.Second)
	snap.FinishedAt = time.Now().UTC()
	fingerprint := "pub-source-" + strings.TrimPrefix(f.scope.Tenant, "pub-t-")
	created, err := f.db.SaveInventoryImport(ctx, snap, fingerprint)
	if err != nil || created {
		t.Fatalf("same fingerprint created new batch: %v", err)
	}
	var observed time.Time
	err = f.db.Pool.QueryRow(ctx, `SELECT w.observed_at FROM source_warehouse_stock_observations w JOIN source_imports i ON i.id=w.import_id WHERE i.source='moysklad' AND i.account_id=$1 ORDER BY i.id DESC LIMIT 1`, f.scope.MoyskladAccount).Scan(&observed)
	if err != nil || !observed.Equal(f.source.FinishedAt) {
		t.Fatalf("reused batch changed observation time: %v", err)
	}
}

func TestStockPublicationFreshDurableTraversalReusesHistoricalBatch(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	var batchID int64
	var fingerprint string
	if err := f.db.Pool.QueryRow(ctx, `SELECT id,fingerprint FROM source_imports WHERE source='moysklad' AND account_id=$1 ORDER BY id DESC LIMIT 1`, f.scope.MoyskladAccount).Scan(&batchID, &fingerprint); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE source_warehouse_stock_observations SET observed_at=$1 WHERE import_id=$2`, old, batchID); err != nil {
		t.Fatal(err)
	}
	if review := f.preview(t, true); review.Ready {
		t.Fatal("old source observation unexpectedly became fresh")
	}
	op, _, err := f.db.SubmitCatalogImport(ctx, f.scope.Tenant, "moysklad", f.scope.MoyskladAccount, "fresh-reused-traversal-"+f.scope.Tenant, "synthetic-session", domain.DefaultMoyskladImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.db.ClaimForCatalogWorker(ctx, time.Minute, "", f.scope.MoyskladAccount)
	if err != nil || task == nil || task.ImportOperationID != op.ID {
		t.Fatalf("claim durable traversal: %+v %v", task, err)
	}
	snap := f.source
	snap.StartedAt = time.Now().UTC()
	snap.FinishedAt = snap.StartedAt.Add(time.Microsecond)
	if err = f.db.CommitClaimedMoyskladImport(ctx, *task, snap, fingerprint, domain.ImportCounts{}, time.Second); err != nil {
		t.Fatal(err)
	}
	completed, err := f.db.GetCatalogOperation(ctx, f.scope.Tenant, "moysklad", f.scope.MoyskladAccount, op.ID)
	if err != nil || completed.BatchID == nil || *completed.BatchID != batchID || completed.BatchCreated == nil || *completed.BatchCreated {
		t.Fatalf("identical content did not reuse its durable batch: %+v %v", completed, err)
	}
	review := f.preview(t, true)
	if !review.Ready || review.Targets[0].Quantity == nil || *review.Targets[0].Quantity != "8" {
		t.Fatalf("fresh completed traversal did not permit the existing calculation: %+v", review.Targets[0].Checks)
	}
	evidence := review.Targets[0].Decision.Evidence.Source
	if evidence.ObservedAt == nil || !evidence.ObservedAt.Equal(old) || evidence.FreshTraversal == nil || evidence.FreshTraversal.OperationID != op.ID || evidence.FreshTraversal.ImportID != batchID {
		t.Fatalf("historical row and scoped traversal not preserved: %+v", evidence)
	}
	var marketID int64
	if err = f.db.Pool.QueryRow(ctx, `SELECT id,fingerprint FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1`, f.scope.OzonAccount).Scan(&marketID, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE ozon_fbs_stock_observations SET observed_at=$1 WHERE import_id=$2`, old, marketID); err != nil {
		t.Fatal(err)
	}
	if review = f.preview(t, true); review.Ready {
		t.Fatal("old marketplace observation unexpectedly became fresh")
	}
	marketOp, _, err := f.db.SubmitCatalogImport(ctx, f.scope.Tenant, "ozon", f.scope.OzonAccount, "fresh-market-traversal-"+f.scope.Tenant, "synthetic-session", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.db.ClaimForCatalogWorker(ctx, time.Minute, f.scope.OzonAccount, "")
	if err != nil || task == nil || task.ImportOperationID != marketOp.ID {
		t.Fatalf("claim marketplace traversal: %+v %v", task, err)
	}
	market := f.market
	market.StartedAt = time.Now().UTC()
	market.FinishedAt = market.StartedAt.Add(time.Microsecond)
	if err = f.db.CommitClaimedOzonImport(ctx, *task, market, fingerprint, domain.ImportCounts{}, time.Second); err != nil {
		t.Fatal(err)
	}
	review = f.preview(t, true)
	marketEvidence := review.Targets[0].Decision.Evidence.Marketplace
	if !review.Ready || marketEvidence.ObservedAt == nil || !marketEvidence.ObservedAt.Equal(old) || marketEvidence.FreshTraversal == nil || marketEvidence.FreshTraversal.OperationID != marketOp.ID || marketEvidence.FreshTraversal.ImportID != marketID {
		t.Fatalf("marketplace batch or traversal not preserved: ready=%v evidence=%+v", review.Ready, marketEvidence)
	}
}

func TestStockPublicationDisableCannotUndoInFlightRequest(t *testing.T) {
	f := publicationData(t, false)
	f.enable(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		fixtureText(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`)
	})
	runner := publicationRunner(f, client)
	done := make(chan error, 1)
	go func() { _, err := runner.RunNext(ctx); done <- err }()
	<-started
	stopped, err := f.db.MutateStockSync(ctx, f.scope, "disable", "", "disable-in-flight-12345", time.Now())
	if err != nil || stopped.Enabled || stopped.State != "STOPPING" {
		t.Fatalf("in-flight stop not represented: state=%s error=%v", stopped.State, err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	stopped, err = f.db.StockSyncStatus(ctx, f.scope, time.Now())
	if err != nil || stopped.Enabled || stopped.LastOperation.Targets[0].State != "SUCCEEDED" || calls.Load() != 1 {
		t.Fatalf("in-flight completion lost: %v", err)
	}
	if worked, err := runner.RunNext(ctx); err != nil || worked || calls.Load() != 1 {
		t.Fatalf("new dispatch after disable: %v %v", worked, err)
	}
}

func TestStockPublicationDisabledWorkerDoesNotClaimPreviouslyEnabledPolicy(t *testing.T) {
	f := publicationData(t, false)
	view := f.enable(t)
	var calls atomic.Int32
	client := publicationClient(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) })
	runner := publicationRunner(f, client)
	runner.Enabled = false
	for i := 0; i < 2; i++ {
		if worked, err := runner.RunNext(context.Background()); err != nil || worked {
			t.Fatalf("disabled worker claimed work: %v %v", worked, err)
		}
	}
	after, err := f.db.StockSyncStatus(context.Background(), f.scope, time.Now())
	if err != nil || after.LastOperation.ID != view.LastOperation.ID || after.LastOperation.State != "QUEUED" || after.LastOperation.Targets[0].DispatchedAt != nil || calls.Load() != 0 {
		t.Fatalf("disabled worker advanced policy or wrote: %v", err)
	}
}

func TestStockPublicationFrozenMappingRevisionChangesBlockDispatch(t *testing.T) {
	for _, table := range []string{"product_mappings", "warehouse_mappings"} {
		t.Run(table, func(t *testing.T) {
			f := publicationData(t, false)
			view := f.enable(t)
			ctx := context.Background()
			// Legacy non-review-bound fixtures can advance; reviewed mappings have
			// additional immutable guards. Simulate an external mapping change.
			sql := `UPDATE ` + table + ` SET version=version+1 WHERE tenant_id=$1`
			tag, err := f.db.Pool.Exec(ctx, sql, f.scope.Tenant)
			if table == "warehouse_mappings" {
				if err == nil {
					t.Fatal("warehouse version constraint accepted version 2")
				}
				tag, err = f.db.Pool.Exec(ctx, `UPDATE warehouse_mappings SET moysklad_warehouse_id='other-warehouse' WHERE tenant_id=$1`, f.scope.Tenant)
			}
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatalf("fixture mapping change: %v", err)
			}
			op, err := f.db.NextStockPublication(ctx, time.Now())
			if err != nil || op != nil {
				t.Fatalf("stale mapping dispatched: %+v %v", op, err)
			}
			var state string
			err = f.db.Pool.QueryRow(ctx, `SELECT state FROM stock_publication_targets WHERE operation_id=$1`, view.LastOperation.ID).Scan(&state)
			if err != nil || state != "BLOCKED" {
				t.Fatalf("mapping change did not block queued target: %s %v", state, err)
			}
		})
	}
}

func TestStockPublicationRejectsMissingReservationAttribution(t *testing.T) {
	f := publicationData(t, false)
	proofs := f.attest[0].Proofs[:0]
	for _, p := range f.attest[0].Proofs {
		if p.Code != commitment.ProofAttribution {
			proofs = append(proofs, p)
		}
	}
	f.attest[0].Proofs = proofs
	review := f.preview(t, true)
	if review.Ready || review.Targets[0].Quantity != nil {
		t.Fatal("missing reservation attribution silently accepted")
	}
	found := false
	for _, c := range review.Targets[0].Checks {
		if c.Code == commitment.ProofAttribution && !c.Passed {
			found = true
		}
	}
	if !found {
		t.Fatal("missing attribution check was not reported")
	}
}

// This helper is executed as a separate synthetic worker process below.
func TestStockPublicationCrashHelper(t *testing.T) {
	if os.Getenv("STYK_AUDIT_CRASH_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	db, err := postgres.Open(context.Background(), os.Getenv("TEST_DATABASE_URL"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Close()
	client, err := ozon.New(ozon.Options{BaseURL: os.Getenv("STYK_AUDIT_SIMULATOR"), ClientID: "synthetic-seller", APIKey: "synthetic-secret", Timeout: 20 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	runner := stocksync.Runner{Enabled: true, Store: db, ResolveOzon: func(context.Context, string, string) (*ozon.Client, error) { return client, nil }}
	if _, err = runner.RunNext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestStockPublicationWorkerCrashAfterDispatchIsNotRetried(t *testing.T) {
	f := publicationData(t, false)
	f.enable(t)
	started, release := make(chan struct{}), make(chan struct{})
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if writes.Add(1) == 1 {
			close(started)
		}
		<-release
		fixtureText(t, w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`)
	}))
	defer server.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestStockPublicationCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "STYK_AUDIT_CRASH_HELPER=1", "STYK_AUDIT_SIMULATOR="+server.URL)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		close(release)
		t.Fatal("synthetic worker did not dispatch")
	}
	if err = cmd.Process.Kill(); err != nil {
		close(release)
		t.Fatal(err)
	}
	_ = cmd.Wait()
	close(release)
	ctx := context.Background()
	view, err := f.db.StockSyncStatus(ctx, f.scope, time.Now())
	if err != nil || view.LastOperation.State != "DISPATCHING" {
		t.Fatalf("crash lost dispatch: %v", err)
	}
	if err = f.db.MarkStockPublicationUnknown(ctx, f.scope, view.LastOperation.ID, time.Now().Add(61*time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: "synthetic-seller", APIKey: "synthetic-secret", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, AllowSyntheticStockWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	runner := publicationRunner(f, client)
	if worked, err := runner.RunNext(ctx); err != nil || worked || writes.Load() != 1 {
		t.Fatalf("crashed worker replayed: %v %v %d", worked, err, writes.Load())
	}
}

func TestStockPublicationRejectsProofAfterSourceTraversalStarted(t *testing.T) {
	f := publicationData(t, false)
	// The alleged confirmation occurs half-way through the read, and before
	// batch completion. The old implementation accepted it as baseline evidence.
	midpoint := f.source.StartedAt.Add(500 * time.Millisecond)
	for i := range f.attest[0].Proofs {
		f.attest[0].Proofs[i].AsOf = midpoint
	}
	review := f.preview(t, true)
	if review.Ready || review.Targets[0].Quantity != nil {
		t.Fatal("proof after traversal start accepted")
	}
	found := false
	for _, c := range review.Targets[0].Checks {
		if c.Code == "PROOF_AFTER_SOURCE_TRAVERSAL_STARTED" && !c.Passed {
			found = true
		}
	}
	if !found {
		t.Fatal("proof ordering failure was not reported")
	}
}
func TestStockPublicationRejectsLongRunningTraversalEvenIfRecentlyFinished(t *testing.T) {
	for _, provider := range []string{"moysklad", "ozon"} {
		t.Run(provider, func(t *testing.T) {
			f := publicationData(t, false)
			now := time.Now().UTC()
			started := now.Add(-16 * time.Minute)
			finished := now.Add(-time.Second)
			if provider == "moysklad" {
				snap := f.source
				snap.StartedAt = started
				snap.FinishedAt = finished
				if _, err := f.db.SaveInventoryImport(context.Background(), snap, "long-source-"+f.scope.Tenant); err != nil {
					t.Fatal(err)
				}
			} else {
				snap := f.market
				snap.StartedAt = started
				snap.FinishedAt = finished
				if _, err := f.db.SaveOzonImport(context.Background(), snap, "long-market-"+f.scope.Tenant); err != nil {
					t.Fatal(err)
				}
			}
			review := f.preview(t, true)
			if review.Ready {
				t.Fatal("old traversal treated as fresh due to recent finish")
			}
			expected := "SOURCE_TRAVERSAL_STALE"
			if provider == "ozon" {
				expected = "OZON_TRAVERSAL_STALE"
			}
			found := false
			for _, target := range review.Targets {
				for _, c := range target.Checks {
					if c.Code == expected && !c.Passed {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("%s not reported", expected)
			}
		})
	}
}
func TestStockPublicationRejectsIdenticalStartTimesForDifferentBatches(t *testing.T) {
	f := publicationData(t, false)
	snap := f.source
	snap.FinishedAt = time.Now().UTC().Add(-time.Second)
	qty := "100"
	snap.WarehouseStock[0].Stock = &qty
	if _, err := f.db.SaveInventoryImport(context.Background(), snap, "same-start-different-content-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	review := f.preview(t, true)
	if review.Ready {
		t.Fatal("indistinguishable traversal order accepted")
	}
	found := false
	for _, c := range review.Checks {
		if c.Code == "OUT_OF_ORDER_IMPORT" && !c.Passed {
			found = true
		}
	}
	if !found {
		t.Fatal("ambiguous ordering not reported")
	}
}
