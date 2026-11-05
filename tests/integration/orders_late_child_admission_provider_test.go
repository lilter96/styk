//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
)

const lateChildNewGrand = "late-child-new-grand"

// The existing provider performs the first two actual split requests. This
// wrapper adds a later split of the retained child, keeping older heads stable.
type lateChildHTTPProvider struct {
	base     *deepOwnedHTTPProvider
	later    atomic.Bool
	attempts atomic.Int32
	writes   atomic.Int32
}

func (p *lateChildHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/posting/fbs/split" && (r.URL.Path != "/v3/posting/fbs/get" || !p.later.Load()) {
		p.base.serve(w, r)
		return
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/v1/posting/fbs/split" && p.base.generation.Load() < 2 {
		p.base.serve(w, r)
		return
	}
	if r.URL.Path == "/v1/posting/fbs/split" {
		p.split(w, r)
		return
	}
	p.posting(w, r)
}

func (p *lateChildHTTPProvider) split(w http.ResponseWriter, r *http.Request) {
	p.attempts.Add(1)
	var body struct {
		Posting  string                 `json:"posting_number"`
		Postings []multiskuSplitPackage `json:"postings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || body.Posting != nestedChild || len(body.Postings) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, posting := range body.Postings {
		if len(posting.Products) != 1 || posting.Products[0].SKU != "2001" || posting.Products[0].Quantity != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	if !p.later.CompareAndSwap(false, true) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	p.writes.Add(1)
	p.base.base.base.reply(w, map[string]any{"parent_posting": mixedRetainedOutput(nestedChild, 2), "postings": []any{mixedRetainedOutput(nestedChild, 1), mixedRetainedOutput(lateChildNewGrand, 1)}})
}

func (p *lateChildHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	var body struct {
		Posting string `json:"posting_number"`
	}
	if err != nil || json.Unmarshal(raw, &body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if body.Posting != nestedChild && body.Posting != lateChildNewGrand {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		p.base.serve(w, r)
		return
	}
	row := p.base.base.base.posting(body.Posting, "FBS")
	row["order_id"], row["status"] = json.Number("5001"), "awaiting_packaging"
	row["delivery_method"] = map[string]any{"id": json.Number("4001"), "warehouse_id": json.Number("3001")}
	row["updated_at"], row["available_actions"] = "2026-11-04T12:01:03Z", []string{"split", "ship"}
	product := row["products"].([]any)[0].(map[string]any)
	product["sku"], product["quantity"], product["vat"] = json.Number("2001"), 1, "0"
	row["requirements"] = multiskuPostingRequirements()
	delete(row, "parent_posting_number")
	p.base.base.base.reply(w, map[string]any{"result": row})
}
