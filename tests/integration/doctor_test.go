//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/doctor"
	"github.com/lilter96/styk/internal/doctordemo"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func TestDoctorSyntheticEndToEndAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Pool.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := postgres.MatchScope{Tenant: "doctor-synthetic-" + suffix, MoyskladAccount: "doctor-moy-" + suffix, OzonAccount: "doctor-ozon-" + suffix}
	if _, err = doctordemo.RunScoped(ctx, s, scope); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	r, err := s.Doctor(ctx, scope, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if r.DecisionStatus != "UNKNOWN" || r.Publication != "DISABLED" || r.Batches["moysklad"].Counts["products"] != 1 || r.Batches["customer_orders"].Counts["orders"] != 1 || r.CorrelationStatuses["CANDIDATE_ONLY"] != 1 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if !strings.Contains(strings.Join(r.DecisionReasons, ","), "SOURCE_BASELINE_NOT_PROVEN") {
		t.Fatal("baseline uncertainty lost")
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), scope.Tenant) || strings.Contains(string(data), "offer") {
		t.Fatal("raw identity leaked")
	}
	scope.Tenant += "-other"
	other, err := s.Doctor(ctx, scope, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if other.Batches["moysklad"].Present || other.DecisionStatus != "NOT_CHECKED" {
		t.Fatal("cross tenant evidence leaked")
	}
}
func TestDoctorEmptyRegisteredAccounts(t *testing.T) {
	f := newStockFixture(t)
	scope := postgres.MatchScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: "empty-m-" + f.req.Pool.Tenant, OzonAccount: "empty-o-" + f.req.Pool.Tenant}
	for _, c := range []struct{ s, a string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := f.store.RegisterStockConnection(context.Background(), scope.Tenant, c.s, c.a, "synthetic empty", true); err != nil {
			t.Fatal(err)
		}
	}
	r, err := f.store.Doctor(context.Background(), scope, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if r.Batches["moysklad"].Present || r.DecisionStatus != "NOT_CHECKED" {
		t.Fatal("missing evidence invented")
	}
	for _, c := range r.Checks {
		if c.Code == "MOYSKLAD_CONNECTION" && c.Status != doctor.NotChecked {
			t.Fatal("offline network claim")
		}
	}
	scope.OzonAccount = f.req.Target.OzonAccount + "-wrong"
	r, err = f.store.Doctor(context.Background(), scope, time.Now(), time.Hour)
	if err != nil || r.Batches["ozon"].Present {
		t.Fatal("incorrect pair accepted")
	}
}

func TestDoctorTruncationAndNoScheduledEffects(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	scope := postgres.MatchScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
	orders := customerOrder(f, "diagnostic-order", nil)
	if _, err := f.store.SaveCustomerOrders(ctx, customerBatch(f, orders)); err != nil {
		t.Fatal(err)
	}
	postings := []domain.PostingObservation{}
	for i := 0; i < 11; i++ {
		p := postingFixture(fmt.Sprintf("diagnostic-%02d", i), "awaiting_packaging", now, domain.PostingLine{OfferID: "offer", SKU: "sku", Quantity: 1})
		p.WarehouseID = f.req.Target.WarehouseID
		postings = append(postings, p)
	}
	if _, err := f.store.SavePostings(ctx, postingBatch(scope.Tenant, scope.OzonAccount, now, postings...)); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	r, err := f.store.Doctor(ctx, scope, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !r.CorrelationsTruncated || r.Batches["postings"].Counts["postings"] != 11 || r.CorrelationStatuses["CANDIDATE_ONLY"] != 10 {
		t.Fatalf("truncation hidden: %+v", r)
	}
	var after, decisions int64
	if err = f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbox_tasks),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, scope.Tenant).Scan(&after, &decisions); err != nil || before != after || decisions != 0 {
		t.Fatal("diagnostic scheduled effects or persisted decisions", err)
	}
}

func TestDoctorRegisteredTenantIsolation(t *testing.T) {
	a, b := newStockFixture(t), newStockFixture(t)
	scope := postgres.MatchScope{Tenant: a.req.Pool.Tenant, MoyskladAccount: a.req.Pool.MoyskladAccount, OzonAccount: b.req.Target.OzonAccount}
	r, err := a.store.Doctor(context.Background(), scope, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Batches["moysklad"].Present || r.Batches["ozon"].Present || r.DecisionStatus != "NOT_CHECKED" {
		t.Fatal("registered accounts from different tenants were paired")
	}
}

func TestDoctorIndependentAccountScopes(t *testing.T) {
	f := newStockFixture(t)
	base := postgres.MatchScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
	for _, tc := range []struct {
		name      string
		scope     postgres.MatchScope
		moy, ozon bool
	}{
		{"both", base, true, true},
		{"moy only", postgres.MatchScope{Tenant: base.Tenant, MoyskladAccount: base.MoyskladAccount, OzonAccount: "not-registered"}, true, false},
		{"ozon only", postgres.MatchScope{Tenant: base.Tenant, MoyskladAccount: "not-registered", OzonAccount: base.OzonAccount}, false, true},
		{"neither", postgres.MatchScope{Tenant: base.Tenant, MoyskladAccount: "missing-m", OzonAccount: "missing-o"}, false, false},
		{"other tenant", postgres.MatchScope{Tenant: base.Tenant + "-other", MoyskladAccount: base.MoyskladAccount, OzonAccount: base.OzonAccount}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := f.store.Doctor(context.Background(), tc.scope, time.Now(), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if r.Batches["moysklad"].Present != tc.moy || r.Batches["ozon"].Present != tc.ozon {
				t.Fatal("independent evidence scope incorrect")
			}
			if !tc.moy || !tc.ozon {
				if r.DecisionStatus != "NOT_CHECKED" || len(r.CorrelationStatuses) != 0 {
					t.Fatal("cross-system analysis ran without both registrations")
				}
				for _, c := range r.Checks {
					if (c.Code == "PRODUCT_MAPPINGS" || c.Code == "WAREHOUSE_MAPPINGS") && c.Status != doctor.NotChecked {
						t.Fatal("cross-system mapping was inferred")
					}
				}
			}
			if r.Publication != "DISABLED" {
				t.Fatal("publication enabled")
			}
		})
	}
}

func TestDoctorCatalogProgressExcludesHistoricalAndOtherAccountMappings(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	scope := postgres.MatchScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
	assertProgress := func(total, confirmed int64) {
		t.Helper()
		r, err := f.store.Doctor(ctx, scope, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if r.MappingCounts["catalog_products"] != total || r.MappingCounts["catalog_confirmed_products"] != confirmed {
			t.Fatalf("unexpected catalog progress: %+v", r.MappingCounts)
		}
	}
	assertProgress(1, 1)
	if err := f.store.RegisterStockConnection(ctx, scope.Tenant, "moysklad", scope.MoyskladAccount+"-other", "synthetic other account", true); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.Pool.Exec(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance) VALUES($1,$2,'removed-product','removed-offer',$3,'variant','item',1,'synthetic historical mapping'),($1,$2,'other-product','other-offer',$4,'variant','item',1,'synthetic other account')`, scope.Tenant, scope.OzonAccount, scope.MoyskladAccount, scope.MoyskladAccount+"-other")
	if err != nil {
		t.Fatal(err)
	}
	assertProgress(1, 1)
	next := f.ozon
	next.StartedAt, next.FinishedAt = time.Now().UTC(), time.Now().UTC()
	next.Products = append(next.Products, domain.OzonProduct{ProductID: "other-product", OfferID: "other-offer"})
	if _, err = f.store.SaveOzonImport(ctx, next, "catalog-progress-next"); err != nil {
		t.Fatal(err)
	}
	assertProgress(2, 1)
	// A newer source row without a completed Ozon batch is not eligible for
	// the catalog page and must not replace its progress denominator.
	if _, err = f.store.Pool.Exec(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('ozon',$1,'synthetic-incomplete',$2,$2,'synthetic')`, scope.OzonAccount, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertProgress(2, 1)
	// Registration history exclusion also applies to the new aggregate keys.
	withoutHistory := newStockFixtureWithHistory(t, false)
	newScope := postgres.MatchScope{Tenant: withoutHistory.req.Pool.Tenant, MoyskladAccount: withoutHistory.req.Pool.MoyskladAccount, OzonAccount: withoutHistory.req.Target.OzonAccount}
	r, err := withoutHistory.store.Doctor(ctx, newScope, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := r.MappingCounts["catalog_products"]; present {
		t.Fatal("ineligible pre-registration catalog was counted")
	}
	scope.Tenant += "-other"
	r, err = f.store.Doctor(ctx, scope, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := r.MappingCounts["catalog_products"]; present {
		t.Fatal("catalog count leaked across tenants")
	}
}
