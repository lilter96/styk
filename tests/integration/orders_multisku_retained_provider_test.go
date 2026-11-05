//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

const multiskuRetainedChild = "multisku-retained-child"

// This local Seller fixture transfers B entirely while retaining A. Its split
// response, rather than an invented provider parent field, supplies provenance.
type multiskuRetainedHTTPProvider struct {
	composition *compositionHTTPProvider
	split       atomic.Bool
	attempts    atomic.Int32
	writes      atomic.Int32
}

func (p *multiskuRetainedHTTPProvider) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/posting/fbs/split" && r.URL.Path != "/v3/posting/fbs/get" {
		p.composition.serve(w, r)
		return
	}
	if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/v1/posting/fbs/split" {
		p.splitPosting(w, r)
		return
	}
	p.posting(w, r)
}

func (p *multiskuRetainedHTTPProvider) splitPosting(w http.ResponseWriter, r *http.Request) {
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
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || body.Posting != nestedRoot || len(body.Postings) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	wanted := map[json.Number]int{"2001": 2, "2002": 3}
	for _, box := range body.Postings {
		if len(box.Products) != 1 || wanted[box.Products[0].SKU] != box.Products[0].Quantity {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		delete(wanted, box.Products[0].SKU)
	}
	if !p.split.CompareAndSwap(false, true) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	p.writes.Add(1)
	p.composition.base.base.reply(w, map[string]any{"parent_posting": multiskuSplitOutput(nestedRoot, true, true), "postings": []any{multiskuSplitOutput(nestedRoot, true, false), multiskuSplitOutput(multiskuRetainedChild, false, true)}})
}

func multiskuSplitOutput(number string, first, second bool) map[string]any {
	products := make([]any, 0, 2)
	if first {
		products = append(products, map[string]any{"product_id": 2001, "quantity": 2})
	}
	if second {
		products = append(products, map[string]any{"product_id": 2002, "quantity": 3})
	}
	return map[string]any{"posting_number": number, "products": products}
}

func (p *multiskuRetainedHTTPProvider) posting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Posting string `json:"posting_number"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Posting != nestedRoot && body.Posting != multiskuRetainedChild && body.Posting != repeatedRetainedIndependent {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if body.Posting == multiskuRetainedChild && !p.split.Load() {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	row := p.composition.base.base.posting(body.Posting, "FBS")
	row["order_id"], row["status"] = json.Number("5001"), "awaiting_packaging"
	row["delivery_method"] = map[string]any{"id": json.Number("4001"), "warehouse_id": json.Number("3001")}
	row["updated_at"], row["available_actions"] = "2026-11-04T12:10:00Z", []string{"split", "ship"}
	row["products"] = multiskuPostingProducts(body.Posting, p.split.Load())
	if p.split.Load() && body.Posting != repeatedRetainedIndependent {
		row["updated_at"] = "2026-11-04T12:11:00Z"
	}
	if body.Posting == repeatedRetainedIndependent {
		row["order_id"] = json.Number("5002")
	}
	row["requirements"] = multiskuPostingRequirements()
	delete(row, "parent_posting_number")
	p.composition.base.base.reply(w, map[string]any{"result": row})
}

func multiskuPostingProducts(number string, split bool) []any {
	first := map[string]any{"sku": json.Number("2001"), "product_id": "1001", "offer_id": "process-offer", "quantity": 2, "price": "12.34", "currency_code": "RUB", "vat": "0"}
	second := map[string]any{"sku": json.Number("2002"), "product_id": "1002", "offer_id": "process-second", "quantity": 3, "price": "4.56", "currency_code": "RUB", "vat": "0"}
	if number == repeatedRetainedIndependent {
		first["quantity"] = 1
		return []any{first}
	}
	if number == multiskuRetainedChild {
		return []any{second}
	}
	if split {
		return []any{first}
	}
	return []any{first, second}
}

func multiskuPostingRequirements() map[string]any {
	result := make(map[string]any)
	for _, key := range []string{"products_requiring_country", "products_requiring_change_country", "products_requiring_gtd", "products_requiring_mandatory_mark", "products_requiring_jw_uin", "products_requiring_rnpt", "products_requiring_imei", "products_requiring_weight"} {
		result[key] = []string{}
	}
	return result
}
