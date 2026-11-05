//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func ownedNestedHTTPFixture(t *testing.T) (positionHTTPFixture, *nestedSettlementHTTPProvider) {
	t.Helper()
	f, p, _ := nestedSettlementHTTPFixture(t, true)
	f.provider.base.server.Config.Handler = http.HandlerFunc(ownedNestedHTTPProvider{p}.serve)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	f.addForeignLine(t)
	nestedSettlementSplit(t, f, nestedRoot, "2")
	rootReduction := splitMemberHTTPPreview(t, f, nestedRoot, nil)
	f.approve(t, rootReduction)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	child := splitMemberHTTPPreview(t, f, nestedChild, nil)
	f.approve(t, child)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	nestedSettlementSplit(t, f, nestedChild, "1")
	_ = unmirroredUnassembledHTTPReview(t, f, nestedRoot)
	return f, p
}

func TestOrdersOwnedNestedRetainedReductionPrecedesGrandchildReserve(t *testing.T) {
	f, p := ownedNestedHTTPFixture(t)
	held := unmirroredUnassembledHTTPReview(t, f, nestedGrandB)
	if held.Decisions[0].Ready {
		t.Fatal("grandchild admitted before actual native reduction")
	}
	reduce := splitMemberHTTPPreview(t, f, nestedChild, nil)
	command := reduce.Decisions[0].Commands[0]
	if command.SplitAncestorSettlement == nil || command.SplitAncestorSettlement.Mode != orders.SplitAncestorReduce || command.Action != orders.ActionMirrorPositionUpdate {
		t.Fatal("missing typed nested owned reduction")
	}
	f.approve(t, reduce)
	f.run(t, orders.StateAcknowledged)
	if held = unmirroredUnassembledHTTPReview(t, f, nestedGrandB); held.Decisions[0].Ready {
		t.Fatal("ACK alone released nested reserve")
	}
	f.run(t, orders.StateReconciled)
	admit := splitMemberHTTPPreview(t, f, nestedGrandB, nil)
	if admit.Decisions[0].Commands[0].SplitAncestorSettlement == nil {
		t.Fatal("missing complete ancestor admission")
	}
	f.approve(t, admit)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertOwnedNestedConserved(t, f)
	if noop := splitMemberHTTPPreview(t, f, nestedGrandB, nil); len(noop.Decisions[0].Commands) != 0 {
		t.Fatal("nested no-op created a duplicate")
	}
	if p.writes.Load() != 2 {
		t.Fatal("a split was replayed")
	}
}

func assertOwnedNestedConserved(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	p := f.provider.base
	p.mu.Lock()
	defer p.mu.Unlock()
	total, foreign := 0, 0
	for _, document := range p.native {
		for _, raw := range document.body["positions"].([]any) {
			row := raw.(map[string]any)
			qty, ok := row["quantity"].(json.Number)
			if !ok {
				t.Fatal("quantity precision lost")
			}
			value, err := qty.Int64()
			if err != nil {
				t.Fatal(err)
			}
			if value == 7 {
				if row["reserve"] != json.Number("2") {
					t.Fatal("foreign reserve changed")
				}
				foreign++
				continue
			}
			reserve, ok := row["reserve"].(json.Number)
			if !ok {
				t.Fatal("reserve absent")
			}
			q, err := reserve.Int64()
			if err != nil || q != value {
				t.Fatal("native owned reserve differs from quantity")
			}
			total += int(q)
		}
	}
	if total != 4 || foreign != 1 || len(p.native) != 3 {
		t.Fatal("nested native frontiers did not conserve four units and foreign reserve")
	}
}

func TestOrdersOwnedNestedUnknownReductionKeepsAncestorFence(t *testing.T) {
	f, p := ownedNestedHTTPFixture(t)
	reduce := splitMemberHTTPPreview(t, f, nestedChild, nil)
	f.approve(t, reduce)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	blocked := unmirroredUnassembledHTTPReview(t, f, nestedGrandB)
	if blocked.Decisions[0].Ready {
		t.Fatal("unknown native reduction released grandchild")
	}
	root := unmirroredUnassembledHTTPReview(t, f, nestedRoot)
	if root.Decisions[0].Ready {
		t.Fatal("UNKNOWN nested effect did not fence root mutation")
	}
	if p.writes.Load() != 2 || f.provider.patches.Load() != 2 {
		t.Fatal("UNKNOWN nested update was replayed")
	}
}
