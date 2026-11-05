//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func deepOwnedHTTPFixture(t *testing.T) (positionHTTPFixture, *deepOwnedHTTPProvider) {
	t.Helper()
	f, _, _ := unmirroredSplitFixture(t)
	p := &deepOwnedHTTPProvider{base: f.provider}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	for _, stage := range []struct {
		parent, child string
		quantity      orders.Decimal
	}{{nestedRoot, nestedChild, "4"}, {nestedChild, deepOwnedGrand, "2"}} {
		nestedSettlementSplit(t, f, stage.parent, stage.quantity)
		deepOwnedMirror(t, f, stage.parent)
		deepOwnedMirror(t, f, stage.child)
	}
	nestedSettlementSplit(t, f, deepOwnedGrand, "1")
	_ = unmirroredUnassembledHTTPReview(t, f, nestedRoot)
	return f, p
}

func deepOwnedMirror(t *testing.T, f positionHTTPFixture, number string) {
	t.Helper()
	preview := splitMemberHTTPPreview(t, f, number, nil)
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
}

func TestOrdersDeepOwnedThirdReductionPrecedesGreatGrandchildReserve(t *testing.T) {
	f, p := deepOwnedHTTPFixture(t)
	held := unmirroredUnassembledHTTPReview(t, f, deepOwnedGreat)
	if held.Decisions[0].Ready {
		t.Fatal("great-grandchild admitted before native ancestor reduction")
	}
	reduce := splitMemberHTTPPreview(t, f, deepOwnedGrand, nil)
	proof := reduce.Decisions[0].Commands[0].SplitAncestorSettlement
	if proof == nil || proof.Mode != orders.SplitAncestorReduce || len(proof.Ancestors) != 3 {
		t.Fatal("missing full three-generation native reduction proof")
	}
	f.approve(t, reduce)
	f.run(t, orders.StateAcknowledged)
	if held = unmirroredUnassembledHTTPReview(t, f, deepOwnedGreat); held.Decisions[0].Ready {
		t.Fatal("ACK without readback released descendant reserve")
	}
	f.run(t, orders.StateReconciled)
	admit := splitMemberHTTPPreview(t, f, deepOwnedGreat, nil)
	if proof = admit.Decisions[0].Commands[0].SplitAncestorSettlement; proof == nil || len(proof.Ancestors) != 3 {
		t.Fatal("descendant admission omitted ancestor frontier")
	}
	f.approve(t, admit)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertOwnedNestedMissingParentProof(t, f.latestWork(t))
	assertDeepOwnedConserved(t, f)
	if noop := splitMemberHTTPPreview(t, f, deepOwnedGreat, nil); len(noop.Decisions[0].Commands) != 0 {
		t.Fatal("unchanged deep descendant created duplicate operation")
	}
	if p.writes.Load() != 3 || p.attempts.Load() != 3 {
		t.Fatal("three-generation split was replayed")
	}
}

func assertDeepOwnedConserved(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	total, foreign := int64(0), 0
	for _, document := range p.native {
		for _, raw := range document.body["positions"].([]any) {
			line := raw.(map[string]any)
			quantity, ok := line["quantity"].(json.Number)
			if !ok {
				t.Fatal("native quantity lost exact representation")
			}
			q, err := quantity.Int64()
			if err != nil {
				t.Fatal(err)
			}
			if q == 7 {
				if line["reserve"] != json.Number("2") {
					t.Fatal("foreign reserve changed")
				}
				foreign++
				continue
			}
			reserve, ok := line["reserve"].(json.Number)
			if !ok {
				t.Fatal("owned reserve unknown")
			}
			r, err := reserve.Int64()
			if err != nil || q != r {
				t.Fatal("native owned reserve differs from physical allocation")
			}
			total += r
		}
	}
	if total != 8 || foreign != 1 || len(p.native) != 4 {
		t.Fatal("deep native frontiers did not conserve eight units and foreign reserve")
	}
}

func TestOrdersDeepOwnedUnknownReductionFencesAncestorsNotIndependentOrder(t *testing.T) {
	f, p := deepOwnedHTTPFixture(t)
	reduce := splitMemberHTTPPreview(t, f, deepOwnedGrand, nil)
	f.approve(t, reduce)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	for _, number := range []string{nestedRoot, nestedChild, deepOwnedGrand, deepOwnedGreat} {
		blocked := unmirroredUnassembledHTTPReview(t, f, number)
		if blocked.Decisions[0].Ready || len(blocked.Decisions[0].Commands) != 0 {
			t.Fatal("UNKNOWN deep reduction did not fence complete ancestor group")
		}
	}
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, deepOwnedIndependent)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, f.ordersStoreFixture, independent, "deep-independent-observation")
	allowed := splitMemberHTTPPreview(t, f, deepOwnedIndependent, nil)
	if !allowed.Decisions[0].Ready || len(allowed.Decisions[0].Commands) != 1 {
		t.Fatal("UNKNOWN ancestor blocked an independent commercial order")
	}
	if f.latestWork(t).ID != unknown.ID || f.latestWork(t).State != orders.StateUnknown || f.latestWork(t).DispatchCount != 1 || p.writes.Load() != 3 || p.attempts.Load() != 3 || f.provider.patches.Load() != 3 {
		t.Fatal("UNKNOWN deep reduction was rewritten or resent")
	}
}
