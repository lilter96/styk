//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lilter96/styk/internal/testpublication"
)

type catalogBrowserProvider struct {
	t                                                  *testing.T
	mu                                                 sync.Mutex
	moyName, moyDescription, ozonName, ozonDescription string
	pendingName                                        string
	moyWrites, ozonWrites, polls                       int
	moyCreates, markerLookups, createdReadbacks        int
	createCode, createSyncID, lastMarker               string
}

func (p *catalogBrowserProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/entity/") || r.URL.Path == "/context/employee" {
		if r.Header.Get("Authorization") != "Bearer synthetic-moy-a" {
			http.Error(w, "synthetic account required", http.StatusForbidden)
			return
		}
		p.moy(w, r)
		return
	}
	if r.Header.Get("Client-Id") != testpublication.OzonAccount || r.Header.Get("Api-Key") != "synthetic-ozon-a" {
		http.Error(w, "synthetic account required", http.StatusForbidden)
		return
	}
	p.ozon(w, r)
}

func (p *catalogBrowserProvider) moy(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/context/employee":
		p.encode(w, map[string]any{"accountId": testpublication.MoyskladAccount})
	case "/entity/product/" + testpublication.SourceProduct:
		if r.Method == http.MethodPut {
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["description"] != catalogBrowserOzonDescription {
				p.fail(w, "MoySklad write widened approved field selection")
				return
			}
			p.moyWrites++
			p.moyDescription = body["description"].(string)
		}
		p.encode(w, p.moyProduct(r))
	case "/entity/product/" + catalogBrowserCreatedID:
		if p.moyCreates != 1 || r.Method != http.MethodGet {
			p.fail(w, "unexpected created-product readback")
			return
		}
		p.createdReadbacks++
		p.encode(w, p.createdMoyProduct(r))
	case "/entity/product", "/entity/variant":
		p.moyCollection(w, r)
	default:
		p.fail(w, "unexpected MoySklad catalog endpoint "+r.URL.Path)
	}
}

func (p *catalogBrowserProvider) moyProduct(r *http.Request) map[string]any {
	return map[string]any{"id": testpublication.SourceProduct, "accountId": testpublication.MoyskladAccount,
		"meta": map[string]any{"href": "http://" + r.Host + "/entity/product/" + testpublication.SourceProduct, "type": "product", "mediaType": "application/json"},
		"name": p.moyName, "description": p.moyDescription, "article": "catalog-browser", "archived": false}
}

func (p *catalogBrowserProvider) ozon(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v3/product/info/list":
		p.ozonInformation(w, r)
	case "/v4/product/info/attributes":
		p.ozonAttributeInformation(w, r)
	case "/v1/product/info/description":
		p.ozonDescriptionInformation(w, r)
	case "/v1/description-category/attribute":
		p.encode(w, map[string]any{"result": []any{p.schemaAttribute(4180, "string"), p.schemaAttribute(4191, "multiline")}})
	case "/v1/product/attributes/update":
		p.ozonUpdate(w, r)
	case "/v1/product/import/info":
		p.ozonPoll(w, r)
	default:
		p.fail(w, "unexpected Ozon catalog endpoint "+r.URL.Path)
	}
}

func (p *catalogBrowserProvider) ozonPoll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID string `json:"task_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.TaskID != "91" {
		p.fail(w, "Ozon poll did not use the persisted task identity")
		return
	}
	p.polls++
	if p.pendingName != "" {
		p.ozonName, p.pendingName = p.pendingName, ""
	}
	p.encode(w, map[string]any{"result": map[string]any{"total": 1, "items": []any{map[string]any{"offer_id": testpublication.OzonOffer, "product_id": json.Number(testpublication.OzonProduct), "status": "imported", "errors": []any{}}}}})
}

func (p *catalogBrowserProvider) ozonInfo(fact catalogBrowserOzonFact) map[string]any {
	return map[string]any{"id": json.Number(fact.id), "offer_id": fact.offer,
		"sku": json.Number(fact.sku), "name": fact.name, "price": "100.00", "currency_code": "RUB", "is_archived": false, "images": []any{}, "vat": "0"}
}

func (p *catalogBrowserProvider) ozonAttributes(fact catalogBrowserOzonFact) map[string]any {
	return map[string]any{"id": json.Number(fact.id), "offer_id": fact.offer,
		"description_category_id": 7, "type_id": 8, "height": 1, "width": 2, "depth": 3,
		"dimension_unit": "mm", "weight": 4, "weight_unit": "g", "attributes": []any{}, "barcodes": []any{}}
}

func (p *catalogBrowserProvider) schemaAttribute(id int64, kind string) map[string]any {
	return map[string]any{"id": id, "name": "Synthetic text", "attribute_complex_id": 0, "type": kind,
		"is_required": false, "is_collection": false, "dictionary_id": 0, "max_value_count": 1}
}

func (p *catalogBrowserProvider) ozonUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			OfferID    string `json:"offer_id"`
			Attributes []struct {
				ID        int64 `json:"id"`
				ComplexID int64 `json:"complex_id"`
				Values    []struct {
					Value        string `json:"value"`
					DictionaryID int64  `json:"dictionary_value_id"`
				} `json:"values"`
			} `json:"attributes"`
		} `json:"items"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || len(body.Items) != 1 || body.Items[0].OfferID != testpublication.OzonOffer || len(body.Items[0].Attributes) != 1 {
		p.fail(w, "Ozon write widened approved product or field selection")
		return
	}
	attribute := body.Items[0].Attributes[0]
	if attribute.ID != 4180 || attribute.ComplexID != 0 || len(attribute.Values) != 1 || attribute.Values[0].DictionaryID != 0 || attribute.Values[0].Value != catalogBrowserSourceName {
		p.fail(w, "Ozon write changed an unapproved field")
		return
	}
	p.ozonWrites++
	p.pendingName = attribute.Values[0].Value
	p.encode(w, map[string]any{"task_id": 91})
}

func (p *catalogBrowserProvider) encode(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		p.t.Errorf("encode synthetic catalog JSON: %v", err)
	}
}

func (p *catalogBrowserProvider) fail(w http.ResponseWriter, message string) {
	p.t.Error(message)
	http.Error(w, "synthetic contract rejected", http.StatusBadRequest)
}
