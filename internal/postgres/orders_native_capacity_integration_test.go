//go:build integration

package postgres

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeConsumptionPostgresRequiresExactResolvedParent(t *testing.T) {
	f, correlation := nativePointStoreFixture(t)
	now, err := ordersClock(f.ctx, f.db.Pool)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := readNativeConsumption(f.ctx, f.db.Pool, correlation, "", "", now)
	if err != nil || !facts.Complete || facts.Correlation != correlation || facts.Scope != correlation.Scope || !facts.CheckedAt.Equal(now) || len(facts.Events) != 0 {
		t.Fatal("resolved parent did not produce bounded complete empty history", err)
	}
	for _, change := range []string{"tenant", "moysklad", "ozon", "posting", "customer", "sync", "marker"} {
		wrong := changedNativeConsumptionCorrelation(correlation, change)
		facts, err = readNativeConsumption(f.ctx, f.db.Pool, wrong, "", "", now)
		if err != nil || facts.Complete || len(facts.Events) != 0 {
			t.Fatalf("%s substitution acquired an empty complete consumption history: %v", change, err)
		}
	}
}

func changedNativeConsumptionCorrelation(correlation orders.Correlation, change string) orders.Correlation {
	switch change {
	case "tenant":
		correlation.Scope.Tenant = "another-tenant"
	case "moysklad":
		correlation.Scope.MoyskladAccount = "another-moysklad"
	case "ozon":
		correlation.Scope.OzonAccount = "another-ozon"
	case "posting":
		correlation.PostingNumber = "another-posting"
	case "customer":
		correlation.CustomerOrderID = "another-customerorder"
	case "sync":
		correlation.SyncID = "another-sync"
	case "marker":
		correlation.Marker = "another-marker"
	}
	return correlation
}
