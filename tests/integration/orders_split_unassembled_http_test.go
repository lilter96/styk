//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

// Ten confirmed allocations fill the existing bounded read page. The original
// posting is intentionally read only on the next normal worker claim.
type unassembledSplitHTTPProvider struct {
	base   *unmirroredSplitHTTPProvider
	writes atomic.Int32
}

func (p *unassembledSplitHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/posting/fbs/split":
		p.split(w, r)
	case "/v3/posting/fbs/get":
		p.posting(w, r)
	case "/v4/posting/fbs/ship":
		w.WriteHeader(http.StatusMethodNotAllowed)
	default:
		p.base.serve(w, r)
	}
}

func (p *unassembledSplitHTTPProvider) split(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(403)
		return
	}
	var body struct {
		Posting  string `json:"posting_number"`
		Postings []struct {
			Products []struct {
				SKU      json.Number `json:"product_id"`
				Quantity int         `json:"quantity"`
			} `json:"products"`
		} `json:"postings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || body.Posting != "position-http-posting" || len(body.Postings) != 10 {
		w.WriteHeader(400)
		return
	}
	for _, posting := range body.Postings {
		if len(posting.Products) != 1 || posting.Products[0].SKU != "2001" || posting.Products[0].Quantity != 1 {
			w.WriteHeader(400)
			return
		}
	}
	p.writes.Add(1)
	p.base.split.Store(true)
	p.base.base.base.reply(w, map[string]any{"parent_posting": map[string]any{"posting_number": body.Posting, "products": []any{map[string]any{"product_id": json.Number("2001"), "quantity": 10}}}, "postings": unassembledHTTPResponseChildren()})
}

func unassembledHTTPResponseChildren() []any {
	children := make([]any, 10)
	for i := range children {
		children[i] = map[string]any{"posting_number": ordersProcessNumber("unassembled-child", i), "products": []any{map[string]any{"product_id": json.Number("2001"), "quantity": 1}}}
	}
	return children
}

func (p *unassembledSplitHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Posting string `json:"posting_number"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	root := body.Posting == "position-http-posting"
	if !root && (!p.base.split.Load() || !slices.ContainsFunc(unassembledHTTPResponseChildren(), func(row any) bool { return row.(map[string]any)["posting_number"] == body.Posting })) {
		w.WriteHeader(404)
		return
	}
	r.Body = io.NopCloser(bytes.NewBufferString(`{"posting_number":"position-http-posting"}`))
	recorder := httptest.NewRecorder()
	p.base.posting(recorder, r)
	var payload map[string]map[string]any
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &payload) != nil {
		w.WriteHeader(500)
		return
	}
	row := payload["result"]
	row["posting_number"], row["status"], row["updated_at"] = body.Posting, "awaiting_packaging", "2026-11-04T12:00:02Z"
	row["products"].([]any)[0].(map[string]any)["quantity"] = 10
	row["available_actions"] = []string{"split", "ship"}
	if p.base.split.Load() {
		p.afterSplit(row, root)
	}
	p.base.base.base.reply(w, payload)
}

func (p *unassembledSplitHTTPProvider) afterSplit(row map[string]any, root bool) {
	// The reviewed official v3 get example includes addressee:null. A null
	// optional group must not poison independent allocation readback evidence.
	row["addressee"] = nil
	row["updated_at"], row["available_actions"] = "2026-11-04T12:00:03Z", []string{}
	if root {
		row["status"] = "cancelled_from_split_pending"
		return
	}
	row["products"].([]any)[0].(map[string]any)["quantity"] = 1
	row["parent_posting_number"] = "position-http-posting"
}

func unassembledHTTPFixture(t *testing.T) (positionHTTPFixture, *unassembledSplitHTTPProvider, orders.Preview) {
	t.Helper()
	f, base, _ := unmirroredSplitFixture(t)
	p := &unassembledSplitHTTPProvider{base: base}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	initial := f.preview(t).Decisions[0].Posting
	packages := make([]orders.ActionPackage, 10)
	for i := range packages {
		packages[i] = orders.ActionPackage{Lines: []orders.ActionQuantity{{Identity: initial.Lines[0].Identity, Quantity: "1"}}}
	}
	proposal := orders.ActionProposal{PostingNumber: initial.PostingNumber, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: packages}}
	return f, p, splitMemberHTTPPreview(t, f, initial.PostingNumber, []orders.ActionProposal{proposal})
}

func TestOrdersUnassembledSplitHTTPResumesNormalizedPagesWithoutReposting(t *testing.T) {
	f, provider, preview := unassembledHTTPFixture(t)
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	ack := f.latestWork(t).Receipt.UnassembledSplit
	f.run(t, orders.StatePolling)
	pending := f.latestWork(t)
	if pending.Receipt.SplitReadCount != 10 || pending.Receipt.ObservedPostings != nil || len(pending.Receipt.PostingNumbers) != 0 {
		t.Fatal("unread original posting was treated as confirmed allocation")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_split_readbacks`, 10)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateReconciled)
	final := f.latestWork(t)
	assertUnassembledHTTPResolved(t, f, final, ack)
	if ran, err := f.runner.RunNext(f.ctx); err != nil || ran {
		t.Fatal("completed split replayed", ran, err)
	}
	if provider.writes.Load() != 1 {
		t.Fatal("unassembled split was POSTed again")
	}
}

func assertUnassembledHTTPResolved(t *testing.T, f positionHTTPFixture, work orders.Work, ack *orders.UnassembledSplitAck) {
	t.Helper()
	if work.Receipt.ObservedPostings != nil || len(work.Receipt.PostingNumbers) != 10 || !slices.IsSorted(work.Receipt.PostingNumbers) || work.DispatchCount != 1 {
		t.Fatal("normalized completion expanded or lost durable split identity")
	}
	original, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	final, err := json.Marshal(work.Receipt.UnassembledSplit)
	if err != nil || !bytes.Equal(original, final) {
		t.Fatal("canonical provider ACK changed", err)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_split_readbacks`, 11)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_reconciliations`, 1)
}
