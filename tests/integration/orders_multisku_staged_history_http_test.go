//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

const multiskuStagedSibling = "multisku-staged-sibling"

// The first split is executed by the existing fixture. This wrapper handles a
// second real Seller command and exact native position URLs, not SQL history.
type multiskuStagedHTTPProvider struct {
	base     *multiskuRetainedHTTPProvider
	root     string
	owned    []string
	second   atomic.Bool
	writes   atomic.Int32
	attempts atomic.Int32
}

func (p *multiskuStagedHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/positions/") {
		p.patch(w, r)
		return
	}
	if r.URL.Path != "/v1/posting/fbs/split" && (r.URL.Path != "/v3/posting/fbs/get" || !p.second.Load()) {
		p.base.serve(w, r)
		return
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/v1/posting/fbs/split" {
		p.split(w, r)
		return
	}
	p.posting(w, r)
}

func (p *multiskuStagedHTTPProvider) split(w http.ResponseWriter, r *http.Request) {
	p.attempts.Add(1)
	var body struct {
		Posting  string                 `json:"posting_number"`
		Postings []multiskuSplitPackage `json:"postings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || body.Posting != nestedRoot || !multiskuStagedPackages(body.Postings) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !p.second.CompareAndSwap(false, true) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	p.writes.Add(1)
	p.base.composition.base.base.reply(w, map[string]any{"parent_posting": multiskuSplitOutput(nestedRoot, 2, 3), "postings": []any{multiskuSplitOutput(nestedRoot, 1, 1), multiskuSplitOutput(multiskuStagedSibling, 1, 2)}})
}

func multiskuStagedPackages(packages []multiskuSplitPackage) bool {
	if len(packages) != 2 {
		return false
	}
	seen := make(map[int]bool, 2)
	for _, box := range packages {
		if len(box.Products) != 2 {
			return false
		}
		units := make(map[json.Number]int, 2)
		for _, product := range box.Products {
			if units[product.SKU] != 0 {
				return false
			}
			units[product.SKU] = product.Quantity
		}
		second := units["2002"]
		if units["2001"] != 1 || second < 1 || second > 2 || seen[second] {
			return false
		}
		seen[second] = true
	}
	return len(seen) == 2
}

func (p *multiskuStagedHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	var body struct {
		Posting string `json:"posting_number"`
	}
	if err != nil || json.Unmarshal(raw, &body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if body.Posting != nestedRoot && body.Posting != multiskuStagedSibling {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		p.base.serve(w, r)
		return
	}
	row := p.base.composition.base.base.posting(body.Posting, "FBS")
	row["order_id"], row["status"] = json.Number("5001"), "awaiting_packaging"
	row["delivery_method"] = map[string]any{"id": json.Number("4001"), "warehouse_id": json.Number("3001")}
	row["updated_at"], row["available_actions"] = "2026-11-04T12:12:00Z", []string{"split", "ship"}
	products := p.base.products(nestedRoot, true)
	products[0].(map[string]any)["quantity"] = 1
	products[1].(map[string]any)["quantity"] = 1
	if body.Posting == multiskuStagedSibling {
		products[1].(map[string]any)["quantity"] = 2
	}
	row["products"], row["requirements"] = products, multiskuPostingRequirements()
	delete(row, "parent_posting_number")
	p.base.composition.base.base.reply(w, map[string]any{"result": row})
}

func (p *multiskuStagedHTTPProvider) patch(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil || len(body) == 0 || len(body) > 3 || !multiskuStagedPatchFields(body) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	base := p.base.composition.base.base
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, native := range base.native {
		if native.id != p.root || r.Header.Get("Authorization") != "Bearer synthetic-orders-moysklad-"+native.account {
			continue
		}
		for index, item := range native.body["positions"].([]any) {
			row := item.(map[string]any)
			id, _ := row["id"].(string)
			if !slices.Contains(p.owned, id) || r.URL.Path != "/entity/customerorder/"+native.id+"/positions/"+id {
				continue
			}
			for field, value := range body {
				row[field] = value
			}
			p.base.composition.base.patches.Add(1)
			base.reply(w, p.base.composition.base.position(native, index))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func multiskuStagedPatchFields(body map[string]any) bool {
	for name, value := range body {
		if name != "quantity" && name != "reserve" && name != "price" {
			return false
		}
		if _, valid := value.(json.Number); !valid {
			return false
		}
	}
	return true
}

func TestOrdersMultiskuStagedHistoryReplaysEveryOwnedScalarMutation(t *testing.T) {
	m := newMultiskuRetainedMode(t, true)
	p := &multiskuStagedHTTPProvider{base: m.p, root: m.original.Correlation.CustomerOrderID, owned: []string{m.first.ID, m.removed.ID}}
	m.f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	firstSplit := m.f.latestWork(t).ID
	origin := multiskuStagedOrigin(t, m)
	firstA := multiskuStagedScalar(t, m, m.first.ID, "2", firstSplit, origin, multiskuRetainedChild)
	multiskuStagedRoot(t, m, "2", "6")
	repeatedRetainedHeld(t, m.f, multiskuRetainedChild)
	firstB := multiskuStagedScalar(t, m, m.removed.ID, "3", firstSplit, firstA, multiskuRetainedChild)
	multiskuStagedRoot(t, m, "2", "3")
	deepOwnedMirror(t, m.f, multiskuRetainedChild)
	multiskuStagedChild(t, m, "2", "3")
	multiskuStagedSecondSplit(t, m)
	secondSplit := m.f.latestWork(t).ID
	secondA := multiskuStagedScalar(t, m, m.first.ID, "1", secondSplit, firstB, multiskuStagedSibling)
	multiskuStagedRoot(t, m, "1", "3")
	repeatedRetainedHeld(t, m.f, multiskuStagedSibling)
	m.f.runner = m.f.restartedRunner(t)
	secondB := multiskuStagedScalar(t, m, m.removed.ID, "1", secondSplit, secondA, multiskuStagedSibling)
	multiskuStagedRoot(t, m, "1", "1")
	deepOwnedMirror(t, m.f, multiskuStagedSibling)
	multiskuStagedChild(t, m, "1", "2")
	multiskuStagedHistory(t, m, []string{firstA, firstB, secondA, secondB})
	multiskuStagedNoop(t, m, p)
}

func multiskuStagedNoop(t *testing.T, m multiskuRetainedFixture, p *multiskuStagedHTTPProvider) {
	t.Helper()
	for _, number := range []string{nestedRoot, multiskuRetainedChild, multiskuStagedSibling} {
		if next := splitMemberHTTPPreview(t, m.f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("staged split history generated another native mutation")
		}
	}
	if m.p.writes.Load() != 1 || m.p.attempts.Load() != 1 || p.writes.Load() != 1 || p.attempts.Load() != 1 || m.f.provider.patches.Load() != 4 || m.p.composition.puts.Load() != 0 {
		t.Fatal("staged history duplicated a split, replayed a patch or silently replaced the collection")
	}
	multiskuStagedConserved(t, m)
}

func multiskuStagedConserved(t *testing.T, m multiskuRetainedFixture) {
	t.Helper()
	base := m.f.provider.base
	base.mu.Lock()
	defer base.mu.Unlock()
	if len(base.native) != 3 {
		t.Fatal("staged history created duplicate native customerorders")
	}
	var total int64
	for _, native := range base.native {
		for _, item := range native.body["positions"].([]any) {
			reserve, known := item.(map[string]any)["reserve"].(json.Number)
			if !known {
				t.Fatal("staged reserve was not retained as an exact number")
			}
			value, err := strconv.ParseInt(reserve.String(), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			total += value
		}
	}
	if total != 11 {
		t.Fatal("staged final reserves did not conserve owned10 plus foreign1")
	}
}

func multiskuStagedScalar(t *testing.T, m multiskuRetainedFixture, position string, quantity orders.Decimal, split, prior, child string) string {
	t.Helper()
	repeatedRetainedHeld(t, m.f, child)
	preview := unmirroredUnassembledHTTPReview(t, m.f, nestedRoot)
	decision := preview.Decisions[0]
	if !decision.Ready || len(decision.Commands) != 1 {
		t.Fatalf("remaining owned scalar update blocked: ready=%v commands=%d validation=%+v", decision.Ready, len(decision.Commands), decision.Validation)
	}
	command := decision.Commands[0]
	patch := command.PositionPatch
	if command.Action != orders.ActionMirrorPositionUpdate || patch == nil || patch.Validate(command) != nil || patch.PositionID != position || patch.Baseline.WorkID != prior || !patch.Quantity.IsKnown() || *patch.Quantity.Value != quantity || !patch.Reserve.IsKnown() || *patch.Reserve.Value != quantity {
		t.Fatal("scalar review lost the exact preceding native UUID frontier")
	}
	actualSplit := command.RetainedSplitWorkID
	if command.SplitAncestorSettlement != nil {
		actualSplit = command.SplitAncestorSettlement.SplitWorkID
	}
	if actualSplit != split {
		t.Fatal("staged scalar update did not retain its actual split authority")
	}
	m.f.approve(t, preview)
	m.f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, m.f, child)
	m.f.run(t, orders.StateReconciled)
	return m.f.latestWork(t).ID
}

func multiskuStagedOrigin(t *testing.T, m multiskuRetainedFixture) string {
	t.Helper()
	history, err := m.f.db.OrderHistory(m.f.ctx, m.f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if work.Command.Action == orders.ActionMirrorCreate && work.Command.PostingNumber == nestedRoot && work.State == orders.StateReconciled {
				return work.ID
			}
		}
	}
	t.Fatal("missing actual reconciled native origin")
	return ""
}

func multiskuStagedRoot(t *testing.T, m multiskuRetainedFixture, firstQuantity, secondQuantity orders.Decimal) {
	t.Helper()
	first, second := m.first, m.removed
	first.Quantity, first.Reserve = firstQuantity, orders.Present(firstQuantity)
	second.Quantity, second.Reserve = secondQuantity, orders.Present(secondQuantity)
	if !orders.NativePositionsEqual(m.readRoot(t).Positions, []orders.TargetPosition{first, second, m.foreign}) {
		t.Fatal("staged scalar update changed native identities or foreign same-SKU reserve")
	}
}

func multiskuStagedChild(t *testing.T, m multiskuRetainedFixture, first, second orders.Decimal) {
	t.Helper()
	child := m.f.readNative(t)
	if child.Correlation.CustomerOrderID == m.original.Correlation.CustomerOrderID || len(child.Positions) != 2 {
		t.Fatal("staged child reused the parent document or lost a line")
	}
	for _, expected := range []struct {
		id       string
		quantity orders.Decimal
	}{{ordersProcessID(8), first}, {ordersProcessID(9), second}} {
		position := multiskuPosition(t, child, expected.id)
		assertRepeatedRetainedPosition(t, child, position.ID, expected.quantity)
	}
}

func multiskuStagedSecondSplit(t *testing.T, m multiskuRetainedFixture) {
	t.Helper()
	source := unmirroredUnassembledHTTPReview(t, m.f, nestedRoot).Decisions[0].Posting
	packages := make([]orders.ActionPackage, 0, 2)
	for _, secondQuantity := range []orders.Decimal{"1", "2"} {
		lines := make([]orders.ActionQuantity, 0, 2)
		for _, line := range source.Lines {
			quantity := orders.Decimal("1")
			if line.Identity.SKU == "2002" {
				quantity = secondQuantity
			}
			lines = append(lines, orders.ActionQuantity{Identity: line.Identity, Quantity: quantity})
		}
		packages = append(packages, orders.ActionPackage{Lines: lines})
	}
	proposal := orders.ActionProposal{PostingNumber: nestedRoot, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: packages}}
	m.f.approve(t, splitMemberHTTPPreview(t, m.f, nestedRoot, []orders.ActionProposal{proposal}))
	m.f.run(t, orders.StateAcknowledged)
	m.f.run(t, orders.StateReconciled)
}

func multiskuStagedHistory(t *testing.T, m multiskuRetainedFixture, ids []string) {
	t.Helper()
	history, err := m.f.db.OrderHistory(m.f.ctx, m.f.config.Scope, orders.PageRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(ids))
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if !slices.Contains(ids, work.ID) {
				continue
			}
			if work.State != orders.StateReconciled || work.DispatchCount != 1 || work.Command.Action != orders.ActionMirrorPositionUpdate {
				t.Fatal("historical staged mutation was rewritten or replayed")
			}
			seen[work.ID] = true
		}
	}
	if len(seen) != len(ids) {
		t.Fatal("staged split history lost a preceding native mutation")
	}
}
