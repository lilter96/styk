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

	"example.com/styk/internal/doctor"
	"example.com/styk/internal/doctordemo"
	"example.com/styk/internal/domain"
	"example.com/styk/internal/postgres"
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
