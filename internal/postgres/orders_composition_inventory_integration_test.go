//go:build integration

package postgres

import (
	"github.com/lilter96/styk/internal/orders"
	"testing"
)

func TestOrderInventoryRemovedSourceLineKeepsOldPoolFencedBeforeComposition(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	posting := orderInventoryPosting(t, fixture, "posting", "awaiting_packaging")
	binding := orderInventoryReconciled(t, fixture, posting, "3")
	orderInventoryNativeBatch(t, fixture, binding, "3", "0")
	baseline := orderInventoryBaseline(t, fixture, "old-pool-baseline", "10", "3")
	guard, err := fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
	if err != nil || guard.Blocked {
		t.Fatalf("initial known source/native reserve: %+v %v", guard, err)
	}
	posting.Lines = []orders.PostingLine{{Identity: orders.LineIdentity{OfferID: "different-offer", SKU: "different-sku"}, ProductID: orders.Present("different-product"), Quantity: "1"}}
	posting.Evidence.Revision = "composition-source-revision"
	posting.Evidence.ObservedAt = fixture.now
	posting.Evidence.ProviderUpdatedAt = orders.Present(fixture.now)
	tx, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if _, err = insertOrderPosting(fixture.ctx, tx, posting, fixture.now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	guard, err = fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
	if err != nil || !guard.Blocked || len(guard.Obligations) != 1 {
		t.Fatalf("removed source identity lost unresolved native reserve before any new intent: %+v %v", guard, err)
	}
	zeroBaseline := orderInventoryBaseline(t, fixture, "old-pool-zero-baseline", "0", "0")
	guard, err = fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, zeroBaseline, fixture.now)
	if err != nil || !guard.Blocked {
		t.Fatalf("explicit stock zero must not silently settle removed position lineage: %+v %v", guard, err)
	}
	guard, err = fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, 0, fixture.now)
	if err != nil || !guard.Blocked {
		t.Fatalf("missing stock observation cleared pending obligation: %+v %v", guard, err)
	}
	other := fixture.request
	other.Pool.ItemID = "independent-product"
	guard, err = fixture.db.OrderInventoryGuard(fixture.ctx, other, 0, fixture.now)
	if err != nil || guard.Blocked || len(guard.Obligations) != 0 {
		t.Fatalf("old pool fence crossed independent product: %+v %v", guard, err)
	}
}
