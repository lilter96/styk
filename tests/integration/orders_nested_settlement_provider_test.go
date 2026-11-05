//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

const nestedRoot = "position-http-posting"
const nestedChild = "nested-child-a"
const nestedSibling = "nested-child-b"
const nestedGrandA = "nested-grand-a"
const nestedGrandB = "nested-grand-b"

type nestedSettlementHTTPProvider struct {
	base                  *unmirroredSplitHTTPProvider
	retainRoot            bool
	rootSplit, childSplit atomic.Bool
	writes                atomic.Int32
}

func (p *nestedSettlementHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/posting/fbs/split":
		p.split(w, r)
	case "/v3/posting/fbs/get":
		p.posting(w, r)
	default:
		p.base.serve(w, r)
	}
}

func (p *nestedSettlementHTTPProvider) split(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Posting  string `json:"posting_number"`
		Postings []struct {
			Products []struct {
				SKU      json.Number `json:"product_id"`
				Quantity int         `json:"quantity"`
			} `json:"products"`
		} `json:"postings"`
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") || json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	quantity, numbers := p.splitOutputs(body.Posting)
	if quantity == 0 || len(body.Postings) != 2 {
		w.WriteHeader(409)
		return
	}
	for _, posting := range body.Postings {
		if len(posting.Products) != 1 || posting.Products[0].SKU != "2001" || posting.Products[0].Quantity != quantity {
			w.WriteHeader(400)
			return
		}
	}
	if body.Posting == nestedRoot {
		p.rootSplit.Store(true)
	} else {
		p.childSplit.Store(true)
	}
	p.writes.Add(1)
	postings := make([]any, 0, 2)
	for _, number := range numbers {
		postings = append(postings, map[string]any{"posting_number": number, "products": []any{map[string]any{"product_id": 2001, "quantity": quantity}}})
	}
	p.base.base.base.reply(w, map[string]any{"parent_posting": map[string]any{"posting_number": body.Posting, "products": []any{map[string]any{"product_id": 2001, "quantity": quantity * 2}}}, "postings": postings})
}

func (p *nestedSettlementHTTPProvider) splitOutputs(number string) (int, []string) {
	if number == nestedRoot && !p.rootSplit.Load() {
		sibling := nestedSibling
		if p.retainRoot {
			sibling = nestedRoot
		}
		return 2, []string{sibling, nestedChild}
	}
	if number == nestedChild && p.rootSplit.Load() && !p.childSplit.Load() {
		return 1, []string{nestedGrandA, nestedGrandB}
	}
	return 0, nil
}

func (p *nestedSettlementHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Posting string `json:"posting_number"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	quantity, status, parent := p.postingFacts(body.Posting)
	if quantity == 0 {
		w.WriteHeader(404)
		return
	}
	row := p.base.base.base.posting(body.Posting, "FBS")
	row["order_id"], row["status"] = json.Number("5001"), status
	row["delivery_method"] = map[string]any{"id": json.Number("4001"), "warehouse_id": json.Number("3001")}
	row["updated_at"] = "2026-11-04T12:01:00Z"
	if p.rootSplit.Load() {
		row["updated_at"] = "2026-11-04T12:01:01Z"
	}
	if p.childSplit.Load() && (body.Posting == nestedChild || body.Posting == nestedGrandA || body.Posting == nestedGrandB) {
		row["updated_at"] = "2026-11-04T12:01:02Z"
	}
	row["available_actions"] = []string{"split", "ship"}
	if status == orders.SupersededSplitParentStatus {
		row["available_actions"] = []string{}
	}
	if parent != "" {
		row["parent_posting_number"] = parent
	}
	product := row["products"].([]any)[0].(map[string]any)
	product["sku"], product["quantity"], product["vat"] = json.Number("2001"), quantity, "0"
	requirements := make(map[string]any)
	for _, key := range []string{"products_requiring_country", "products_requiring_change_country", "products_requiring_gtd", "products_requiring_mandatory_mark", "products_requiring_jw_uin", "products_requiring_rnpt", "products_requiring_imei", "products_requiring_weight"} {
		requirements[key] = []string{}
	}
	row["requirements"] = requirements
	p.base.base.base.reply(w, map[string]any{"result": row})
}

func (p *nestedSettlementHTTPProvider) postingFacts(number string) (int, string, string) {
	switch number {
	case nestedRoot:
		if !p.rootSplit.Load() {
			return 4, "awaiting_packaging", ""
		}
		if p.retainRoot {
			return 2, "awaiting_packaging", ""
		}
		return 4, orders.SupersededSplitParentStatus, ""
	case nestedChild:
		if !p.rootSplit.Load() {
			return 0, "", ""
		}
		if p.childSplit.Load() {
			return 2, orders.SupersededSplitParentStatus, nestedRoot
		}
		return 2, "awaiting_packaging", nestedRoot
	case nestedSibling:
		if p.rootSplit.Load() && !p.retainRoot {
			return 2, "awaiting_packaging", nestedRoot
		}
	case nestedGrandA, nestedGrandB:
		if p.childSplit.Load() {
			return 1, "awaiting_packaging", nestedChild
		}
	}
	return 0, "", ""
}

func nestedSettlementHTTPFixture(t *testing.T, retained bool) (positionHTTPFixture, *nestedSettlementHTTPProvider, orders.Posting) {
	t.Helper()
	f, base, _ := unmirroredSplitFixture(t)
	p := &nestedSettlementHTTPProvider{base: base, retainRoot: retained}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	return f, p, f.preview(t).Decisions[0].Posting
}
