//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func ownedNestedHTTPFixture(t *testing.T) (positionHTTPFixture, *nestedSettlementHTTPProvider, *ownedNestedParentHTTPProvider) {
	t.Helper()
	f, p, _ := nestedSettlementHTTPFixture(t, true)
	wire := &ownedNestedParentHTTPProvider{base: ownedNestedHTTPProvider{p}, t: t}
	f.provider.base.server.Config.Handler = http.HandlerFunc(wire.serve)
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
	return f, p, wire
}

func TestOrdersOwnedNestedRetainedReductionPrecedesGrandchildReserve(t *testing.T) {
	f, p, _ := ownedNestedHTTPFixture(t)
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
	assertOwnedNestedMissingParentProof(t, f.latestWork(t))
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
	assertOwnedNestedMissingParentProof(t, f.latestWork(t))
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
	f, p, _ := ownedNestedHTTPFixture(t)
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

// The official parent_posting_number is optional. These two existing journeys
// exercise native-owned settlement using immutable local split lineage without
// inventing a provider parent field in observations or frozen commands.
type ownedNestedParentHTTPProvider struct {
	base                  ownedNestedHTTPProvider
	t                     *testing.T
	conflictingGrandchild atomic.Bool
	conflictingReads      atomic.Int32
}

func (p *ownedNestedParentHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v3/posting/fbs/get" {
		p.base.serve(w, r)
		return
	}
	recorded := httptest.NewRecorder()
	p.base.serve(recorded, r)
	if recorded.Code != http.StatusOK {
		w.WriteHeader(recorded.Code)
		return
	}
	var body, row map[string]json.RawMessage
	if json.Unmarshal(recorded.Body.Bytes(), &body) != nil || json.Unmarshal(body["result"], &row) != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	delete(row, "parent_posting_number")
	if p.conflictingGrandchild.Load() && string(row["posting_number"]) == `"nested-grand-b"` {
		// A real ancestor is still the wrong direct parent of this generation.
		row["parent_posting_number"] = json.RawMessage(strconv.Quote(nestedRoot))
		p.conflictingReads.Add(1)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		p.t.Error("encode synthetic optional-parent posting")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	body["result"] = raw
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(body); err != nil {
		p.t.Error("write synthetic optional-parent posting")
	}
}

func assertOwnedNestedMissingParentProof(t *testing.T, work orders.Work) {
	t.Helper()
	proof := work.Command.SplitAncestorSettlement
	if proof == nil {
		t.Fatal("missing durable local ancestry permission")
	}
	postings := []orders.Posting{proof.Before, proof.Node, proof.Source}
	for _, ancestor := range proof.Ancestors {
		postings = append(postings, ancestor.Posting)
	}
	for _, posting := range postings {
		if posting.ParentPostingNumber.Presence != orders.PresenceMissing || posting.ParentPostingNumber.Value != nil {
			t.Fatal("local lineage fabricated a provider parent observation")
		}
	}
}

func TestOrdersOwnedNestedConflictingFreshGrandchildParentBlocksDispatch(t *testing.T) {
	f, p, wire := ownedNestedHTTPFixture(t)
	reduce := splitMemberHTTPPreview(t, f, nestedChild, nil)
	f.approve(t, reduce)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	admit := splitMemberHTTPPreview(t, f, nestedGrandB, nil)
	f.approve(t, admit)
	wire.conflictingGrandchild.Store(true)
	f.run(t, orders.StateBlocked)
	work := f.latestWork(t)
	if wire.conflictingReads.Load() == 0 || work.DispatchCount != 0 || p.writes.Load() != 2 || f.provider.patches.Load() != 2 {
		t.Fatal("contradictory fresh provider ancestry permitted a write or replay")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 2 {
		t.Fatal("conflicting grandchild created a native order")
	}
}
