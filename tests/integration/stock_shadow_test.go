//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

type shadowResponse struct {
	Targets []struct {
		ProductID           string                  `json:"product_id"`
		Physical            *string                 `json:"physical"`
		Reserve             *string                 `json:"reserve"`
		Candidate           *string                 `json:"candidate"`
		SourceBatchStarted  *time.Time              `json:"source_batch_started"`
		SourceBatchFinished *time.Time              `json:"source_batch_finished"`
		Uncertain           bool                    `json:"uncertain_write"`
		ExpiredEvidence     bool                    `json:"expired_evidence"`
		Checks              []domain.StockSyncCheck `json:"checks"`
	} `json:"targets"`
}

func shadowClient(t *testing.T, f publicationFixture) func(domain.StockSyncScope, int) shadowResponse {
	t.Helper()
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "")
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "http://" + server.Listener.Addr().String()
	secret := strings.Repeat("x", 32)
	handler, err := console.Handler(f.db, secret, origin)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar
	req, _ := http.NewRequest("POST", origin+"/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("session: %d", resp.StatusCode)
	}
	return func(scope domain.StockSyncScope, status int) shadowResponse {
		t.Helper()
		q := url.Values{"tenant": {scope.Tenant}, "moysklad_account": {scope.MoyskladAccount}, "ozon_account": {scope.OzonAccount}}
		resp, err := client.Get(origin + "/api/stock-sync/shadow?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("shadow returned %d, expected %d", resp.StatusCode, status)
		}
		var result shadowResponse
		if status == 200 {
			if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
}
func shadowCheck(t *testing.T, result shadowResponse, code string) {
	t.Helper()
	for _, target := range result.Targets {
		for _, check := range target.Checks {
			if check.Code == code && !check.Passed {
				return
			}
		}
	}
	t.Fatalf("missing failed %s: %+v", code, result)
}
func TestStockShadowProjectionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stock, reserve *string
		omit           bool
		code           string
	}{
		{name: "explicit zero", stock: ptr("0"), reserve: ptr("0")},
		{name: "missing observation", omit: true, code: "SOURCE_OBSERVATION_MISSING"},
		// The schema requires both values. A missing reserve therefore makes
		// the observation unavailable rather than supplying an implicit zero.
		{name: "missing reserve", omit: true, code: "SOURCE_RESERVE_MISSING"},
		{name: "negative stock", stock: ptr("-1"), reserve: ptr("0"), code: "PHYSICAL_STOCK_UNSUPPORTED"},
		{name: "fractional stock", stock: ptr("1.5"), reserve: ptr("0"), code: "PHYSICAL_STOCK_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := publicationData(t, false)
			ctx := context.Background()
			snap := f.source
			snap.StartedAt = time.Now().UTC().Add(-10 * time.Second)
			snap.FinishedAt = snap.StartedAt.Add(time.Second)
			snap.WarehouseStock = append([]domain.WarehouseStockObservation(nil), f.source.WarehouseStock...)
			if tc.name == "missing reserve" {
				incomplete := snap
				incomplete.WarehouseStock = append([]domain.WarehouseStockObservation(nil), snap.WarehouseStock...)
				incomplete.WarehouseStock[0].Reserve = nil
				if _, err := f.db.SaveInventoryImport(ctx, incomplete, "shadow-null-reserve-"+f.scope.Tenant); err == nil {
					t.Fatal("the database accepted a stock row without reserve")
				}
			}
			if tc.omit {
				snap.WarehouseStock = nil
			} else {
				snap.WarehouseStock[0].Stock = tc.stock
				snap.WarehouseStock[0].Reserve = tc.reserve
			}
			if _, err := f.db.SaveInventoryImport(ctx, snap, "shadow-"+tc.name+f.scope.Tenant); err != nil {
				t.Fatal(err)
			}
			if tc.name == "explicit zero" {
				f.attest[0].SafetyStock = "0"
				f.enable(t)
			}
			get := shadowClient(t, f)
			var before, after [4]int
			counts := func(out *[4]int) {
				t.Helper()
				if err := f.db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_sync_reviews WHERE tenant_id=$1),(SELECT count(*) FROM stock_sync_mutations WHERE tenant_id=$1),(SELECT count(*) FROM stock_publications WHERE tenant_id=$1),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, f.scope.Tenant).Scan(&out[0], &out[1], &out[2], &out[3]); err != nil {
					t.Fatal(err)
				}
			}
			counts(&before)
			got := get(f.scope, 200)
			counts(&after)
			if before != after {
				t.Fatalf("read mutated state: %v -> %v", before, after)
			}
			view, err := f.db.StockSyncStatus(ctx, f.scope, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Targets) != 1 || len(view.Targets) != 1 {
				t.Fatal("wrong target scope")
			}
			actual, want := got.Targets[0].Candidate, view.Targets[0].Quantity
			if (actual == nil) != (want == nil) || (actual != nil && *actual != *want) {
				t.Fatal("shadow disagrees with authoritative review")
			}
			if tc.code != "" {
				shadowCheck(t, got, tc.code)
				if actual != nil {
					t.Fatal("invalid calculation exposed candidate")
				}
			}
			if tc.name == "explicit zero" && (got.Targets[0].Physical == nil || *got.Targets[0].Physical != "0" || actual == nil || *actual != "0") {
				t.Fatal("explicit zero was lost")
			}
			other := f.scope
			other.Tenant = "other-tenant"
			get(other, 404)
			other = f.scope
			other.MoyskladAccount = "other-account"
			get(other, 404)
			other = f.scope
			other.OzonAccount = "other-account"
			get(other, 404)
		})
	}
}
func ptr(s string) *string { return &s }

func TestStockShadowRejectsParentVariantOverlap(t *testing.T) {
	f := publicationData(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	source := f.source
	source.StartedAt, source.FinishedAt = now.Add(-10*time.Second), now.Add(-time.Second)
	source.Entities = []domain.ExternalEntity{{Kind: "product", ID: "parent", Article: "parent-offer"}, {Kind: "variant", ID: "item", ParentID: "parent", Article: "offer"}, {Kind: "store", ID: "warehouse"}}
	parentStock := source.WarehouseStock[0]
	parentStock.Kind, parentStock.ExternalID = "product", "parent"
	source.WarehouseStock = append(source.WarehouseStock, parentStock)
	if _, err := f.db.SaveInventoryImport(ctx, source, "shadow-overlap-source-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	market := f.market
	market.StartedAt, market.FinishedAt = now.Add(-10*time.Second), now.Add(-time.Second)
	archived := false
	market.Products = append(market.Products, domain.OzonProduct{ProductID: "124", OfferID: "parent-offer", SKU: "322", Archived: &archived})
	market.Stocks = append(market.Stocks, domain.OzonStock{ProductID: "124", OfferID: "parent-offer", SKU: "322", WarehouseID: "456", Present: 5, Reserved: 1, FreeStock: 4})
	if _, err := f.db.SaveOzonImport(ctx, market, "shadow-overlap-market-"+f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: f.scope.Tenant, MoyskladAccount: f.scope.MoyskladAccount, OzonAccount: f.scope.OzonAccount}
	if _, err := f.db.ConfirmMatch(ctx, match, "124", "product", "parent", "synthetic parent mapping"); err != nil {
		t.Fatal(err)
	}
	got := shadowClient(t, f)(f.scope, 200)
	if len(got.Targets) != 2 {
		t.Fatalf("unexpected targets: %d", len(got.Targets))
	}
	for _, target := range got.Targets {
		if target.Candidate != nil {
			t.Fatal("overlapping pool produced a candidate")
		}
		found := false
		for _, c := range target.Checks {
			if c.Code == "SHARED_POOL_UNSUPPORTED" && !c.Passed {
				found = true
			}
		}
		if !found {
			t.Fatal("shared pool blocker not projected")
		}
	}
}

func TestStockShadowStaleReusedAndUnknown(t *testing.T) {
	t.Run("reused batch", func(t *testing.T) {
		f := publicationData(t, false)
		get := shadowClient(t, f)
		before := get(f.scope, 200)
		snap := f.source
		snap.StartedAt = time.Now().UTC().Add(-time.Second)
		snap.FinishedAt = time.Now().UTC()
		fingerprint := "pub-source-" + strings.TrimPrefix(f.scope.Tenant, "pub-t-")
		created, err := f.db.SaveInventoryImport(context.Background(), snap, fingerprint)
		if err != nil || created {
			t.Fatalf("reused import: %v %v", created, err)
		}
		after := get(f.scope, 200)
		if !after.Targets[0].SourceBatchStarted.Equal(*before.Targets[0].SourceBatchStarted) || !after.Targets[0].SourceBatchFinished.Equal(*before.Targets[0].SourceBatchFinished) {
			t.Fatal("batch reuse refreshed timestamps")
		}
	})
	t.Run("stale traversal", func(t *testing.T) {
		f := publicationData(t, false)
		ctx := context.Background()
		_, err := f.db.Pool.Exec(ctx, `UPDATE source_imports SET started_at=$1 WHERE source='moysklad' AND account_id=$2`, time.Now().UTC().Add(-20*time.Minute), f.scope.MoyskladAccount)
		if err != nil {
			t.Fatal(err)
		}
		got := shadowClient(t, f)(f.scope, 200)
		shadowCheck(t, got, "SOURCE_TRAVERSAL_STALE")
		if got.Targets[0].Candidate != nil {
			t.Fatal("stale traversal produced candidate")
		}
	})
	t.Run("unknown write", func(t *testing.T) {
		f := publicationData(t, false)
		f.enable(t)
		ctx := context.Background()
		now := time.Now().UTC()
		op, err := f.db.NextStockPublication(ctx, now)
		if err != nil || op == nil {
			t.Fatalf("claim: %v", err)
		}
		if err = f.db.MarkStockPublicationUnknown(ctx, f.scope, op.ID, now.Add(61*time.Second)); err != nil {
			t.Fatal(err)
		}
		got := shadowClient(t, f)(f.scope, 200)
		if len(got.Targets) != 1 || !got.Targets[0].Uncertain {
			t.Fatal("uncertain target hidden")
		}
	})
	t.Run("expired attestation", func(t *testing.T) {
		f := publicationData(t, false)
		deadline := time.Now().UTC().Add(time.Second)
		for i := range f.attest[0].Proofs {
			f.attest[0].Proofs[i].ValidUntil = deadline
		}
		f.enable(t)
		time.Sleep(time.Until(deadline) + 20*time.Millisecond)
		got := shadowClient(t, f)(f.scope, 200)
		if got.Targets[0].Candidate != nil || !got.Targets[0].ExpiredEvidence {
			t.Fatal("expired evidence produced candidate")
		}
		shadowCheck(t, got, "COMPETING_EXPOSURE_NOT_VERIFIED")
	})
}
