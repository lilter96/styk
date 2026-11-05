//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
)

const deepOwnedGrand = "deep-owned-grand"
const deepOwnedGreat = "deep-owned-great"
const deepOwnedIndependent = "deep-owned-independent"

// All bodies are generated local contract fixtures. The absent provider parent
// remains absent: acknowledged split outputs establish the local ancestry.
type deepOwnedHTTPProvider struct {
	base       *positionHTTPProvider
	generation atomic.Int32
	writes     atomic.Int32
	attempts   atomic.Int32
}

func (p *deepOwnedHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/posting/fbs/split" && r.URL.Path != "/v3/posting/fbs/get" {
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

func deepOwnedGeneration(generation int32) (string, string, int) {
	switch generation {
	case 0:
		return nestedRoot, nestedChild, 4
	case 1:
		return nestedChild, deepOwnedGrand, 2
	case 2:
		return deepOwnedGrand, deepOwnedGreat, 1
	default:
		return "", "", 0
	}
}

func (p *deepOwnedHTTPProvider) split(w http.ResponseWriter, r *http.Request) {
	p.attempts.Add(1)
	var body struct {
		Posting  string `json:"posting_number"`
		Postings []struct {
			Products []struct {
				SKU      json.Number `json:"product_id"`
				Quantity int         `json:"quantity"`
			} `json:"products"`
		} `json:"postings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	stage := p.generation.Load()
	parent, child, quantity := deepOwnedGeneration(stage)
	if body.Posting != parent || quantity == 0 || len(body.Postings) != 2 {
		w.WriteHeader(409)
		return
	}
	for _, box := range body.Postings {
		if len(box.Products) != 1 || box.Products[0].SKU != "2001" || box.Products[0].Quantity != quantity {
			w.WriteHeader(400)
			return
		}
	}
	if !p.generation.CompareAndSwap(stage, stage+1) {
		w.WriteHeader(409)
		return
	}
	p.writes.Add(1)
	postings := make([]any, 0, 2)
	for _, number := range []string{parent, child} {
		postings = append(postings, map[string]any{"posting_number": number, "products": []any{map[string]any{"product_id": 2001, "quantity": quantity}}})
	}
	p.base.base.reply(w, map[string]any{"parent_posting": map[string]any{"posting_number": parent, "products": []any{map[string]any{"product_id": 2001, "quantity": quantity * 2}}}, "postings": postings})
}

func (p *deepOwnedHTTPProvider) quantity(number string) int {
	stage := p.generation.Load()
	switch number {
	case nestedRoot:
		if stage == 0 {
			return 8
		}
		return 4
	case nestedChild:
		if stage < 1 {
			return 0
		}
		if stage == 1 {
			return 4
		}
		return 2
	case deepOwnedGrand:
		if stage < 2 {
			return 0
		}
		if stage == 2 {
			return 2
		}
		return 1
	case deepOwnedGreat:
		if stage == 3 {
			return 1
		}
	case deepOwnedIndependent:
		return 1
	}
	return 0
}

func (p *deepOwnedHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Posting string `json:"posting_number"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	quantity := p.quantity(body.Posting)
	if quantity == 0 {
		w.WriteHeader(404)
		return
	}
	row := p.base.base.posting(body.Posting, "FBS")
	row["order_id"], row["status"] = json.Number("5001"), "awaiting_packaging"
	if body.Posting == deepOwnedIndependent {
		row["order_id"] = json.Number("5002")
	}
	row["delivery_method"] = map[string]any{"id": json.Number("4001"), "warehouse_id": json.Number("3001")}
	row["updated_at"] = fmt.Sprintf("2026-11-04T12:01:0%dZ", p.revision(body.Posting))
	row["available_actions"] = []string{"split", "ship"}
	product := row["products"].([]any)[0].(map[string]any)
	product["sku"], product["quantity"], product["vat"] = json.Number("2001"), quantity, "0"
	requirements := make(map[string]any)
	for _, key := range []string{"products_requiring_country", "products_requiring_change_country", "products_requiring_gtd", "products_requiring_mandatory_mark", "products_requiring_jw_uin", "products_requiring_rnpt", "products_requiring_imei", "products_requiring_weight"} {
		requirements[key] = []string{}
	}
	row["requirements"] = requirements
	delete(row, "parent_posting_number")
	p.base.base.reply(w, map[string]any{"result": row})
}

func (p *deepOwnedHTTPProvider) revision(number string) int32 {
	stage := p.generation.Load()
	switch number {
	case nestedRoot:
		return min(stage, 1)
	case nestedChild:
		return min(stage, 2)
	case deepOwnedGrand, deepOwnedGreat:
		return stage
	default:
		return 0
	}
}
