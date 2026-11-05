//go:build integration

package postgres

import (
	"testing"
	"time"

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

// A missing known ancestor blocks only its descendants. Omitting a parent in a
// later short observation cannot erase that durable relation.
func TestNativeConsumptionPostgresKeepsKnownAncestryScoped(t *testing.T) {
	f, _ := nativePointStoreFixture(t)
	child := orderInventoryPosting(t, f, "external-child", "awaiting_packaging")
	independent := orderInventoryPosting(t, f, "independent-same-order", "awaiting_packaging")
	child.ParentPostingNumber = orders.Present("unobserved-parent")
	child.Evidence.Revision = "child-known-parent"
	child.Evidence.ProviderUpdatedAt = orders.Present(f.now)
	saveNativeConsumptionPosting(t, f, child)
	child.ParentPostingNumber = orders.Field[string]{}
	child.Evidence.Revision = "child-omitted-parent"
	child.Evidence.ProviderUpdatedAt = orders.Present(f.now.Add(time.Second))
	saveNativeConsumptionPosting(t, f, child)
	current := orders.ReviewContext{Config: f.config, Postings: []orders.Posting{child, independent}}
	if err := enrichNativeSplitConsumption(f.ctx, f.db.Pool, &current, f.now); err != nil {
		t.Fatal(err)
	}
	if current.NativeInventoryActionBlocked(orders.ActionPostingSplit, child.PostingNumber) || !current.NativeInventoryActionBlocked(orders.ActionMirrorCreate, child.PostingNumber) || !current.NativeInventoryActionBlocked(orders.ActionDemandCreate, child.PostingNumber) || current.NativeSplitBlocked[independent.PostingNumber] || current.NativeAncestorConsumed[independent.PostingNumber] {
		t.Fatal("ancestry omission reopened child or blocked an unrelated posting")
	}
}

func saveNativeConsumptionPosting(t *testing.T, f orderInventoryFixture, posting orders.Posting) {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if _, err = insertOrderPosting(f.ctx, tx, posting, f.now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}
