//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

func (p *catalogBrowserProvider) moyCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/entity/product" {
		p.moyCreate(w, r)
		return
	}
	rows := make([]any, 0, 2)
	filter := r.URL.Query().Get("filter")
	if filter != "" {
		p.markerLookups++
		p.lastMarker = strings.TrimSuffix(strings.TrimPrefix(filter, "externalCode="), ";archived=true;archived=false")
		if p.moyCreates == 1 && p.lastMarker == p.createCode {
			rows = append(rows, p.createdMoyProduct(r))
		}
	} else if r.URL.Path == "/entity/product" {
		rows = append(rows, p.moyProduct(r))
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		p.fail(w, "unbounded MoySklad catalog traversal")
		return
	}
	p.encode(w, map[string]any{"meta": map[string]any{"size": len(rows), "offset": 0, "limit": limit}, "rows": rows})
}

func (p *catalogBrowserProvider) moyCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 4 || body["name"] != catalogBrowserUnmappedName || body["description"] != catalogBrowserUnmappedDescription {
		p.fail(w, "creation widened or replaced the approved source facts")
		return
	}
	if p.moyCreates != 0 || body["syncId"] == "" || body["syncId"] == catalogBrowserCreatedID || body["externalCode"] != "STYK-CATALOG-"+body["syncId"] || body["externalCode"] != p.lastMarker {
		p.fail(w, "creation did not preserve a unique durable marker")
		return
	}
	p.moyCreates++
	p.createSyncID, p.createCode = body["syncId"], body["externalCode"]
	w.WriteHeader(http.StatusCreated)
	p.encode(w, p.createdMoyProduct(r))
}

func (p *catalogBrowserProvider) createdMoyProduct(r *http.Request) map[string]any {
	return map[string]any{"id": catalogBrowserCreatedID, "accountId": testpublication.MoyskladAccount,
		"meta": map[string]any{"href": "http://" + r.Host + "/entity/product/" + catalogBrowserCreatedID, "type": "product", "mediaType": "application/json"},
		"name": catalogBrowserUnmappedName, "description": catalogBrowserUnmappedDescription, "archived": false,
		"externalCode": p.createCode, "syncId": p.createSyncID}
}

type catalogBrowserOzonFact struct{ id, offer, sku, name, description string }

func (p *catalogBrowserProvider) ozonFact(id string) (catalogBrowserOzonFact, bool) {
	switch id {
	case testpublication.OzonProduct:
		return catalogBrowserOzonFact{id, testpublication.OzonOffer, testpublication.OzonSKU, p.ozonName, p.ozonDescription}, true
	case catalogBrowserUnmappedID:
		return catalogBrowserOzonFact{id, catalogBrowserUnmappedOffer, "8080000002", catalogBrowserUnmappedName, catalogBrowserUnmappedDescription}, true
	default:
		return catalogBrowserOzonFact{}, false
	}
}

func (p *catalogBrowserProvider) selectedOzonFacts(r *http.Request) ([]catalogBrowserOzonFact, bool) {
	var body struct {
		IDs    []string `json:"product_id"`
		Filter struct {
			IDs []string `json:"product_id"`
		} `json:"filter"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		return nil, false
	}
	ids := append(body.IDs, body.Filter.IDs...)
	if len(ids) != 1 {
		return nil, false
	}
	fact, ok := p.ozonFact(ids[0])
	return []catalogBrowserOzonFact{fact}, ok
}

func (p *catalogBrowserProvider) ozonInformation(w http.ResponseWriter, r *http.Request) {
	facts, ok := p.selectedOzonFacts(r)
	if !ok {
		p.fail(w, "Ozon information read did not select an exact product")
		return
	}
	p.encode(w, map[string]any{"items": []any{p.ozonInfo(facts[0])}})
}

func (p *catalogBrowserProvider) ozonAttributeInformation(w http.ResponseWriter, r *http.Request) {
	facts, ok := p.selectedOzonFacts(r)
	if !ok {
		p.fail(w, "Ozon attribute read did not select an exact product")
		return
	}
	p.encode(w, map[string]any{"total": "1", "result": []any{p.ozonAttributes(facts[0])}})
}

func (p *catalogBrowserProvider) ozonDescriptionInformation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      json.Number `json:"product_id"`
		OfferID string      `json:"offer_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		p.fail(w, "invalid description read identity")
		return
	}
	fact, ok := p.ozonFact(body.ID.String())
	if !ok || body.OfferID != fact.offer {
		p.fail(w, "description read conflated product and offer identity")
		return
	}
	p.encode(w, map[string]any{"result": map[string]any{"id": json.Number(fact.id), "offer_id": fact.offer, "description": fact.description}})
}

func assertCatalogBrowserCreation(t *testing.T, db *postgres.Store, simulator *catalogBrowserProvider) {
	t.Helper()
	if simulator.moyCreates != 1 || simulator.markerLookups < 2 || simulator.createdReadbacks == 0 {
		t.Fatalf("creation was not uniquely dispatched and read back: creates=%d marker_reads=%d readbacks=%d", simulator.moyCreates, simulator.markerLookups, simulator.createdReadbacks)
	}
	var actual, syncID string
	err := db.Pool.QueryRow(context.Background(), `SELECT work->'created_target'->>'id',work->'command'->>'create_id' FROM catalog_sync_work WHERE tenant_id=$1 AND work->'command'->>'action'='create'`, testpublication.Tenant).Scan(&actual, &syncID)
	if err != nil || actual != catalogBrowserCreatedID || syncID != simulator.createSyncID || actual == syncID {
		t.Fatalf("actual provider identity was not persisted separately from syncId: actual=%q err=%v", actual, err)
	}
	var mappings int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM product_mappings WHERE tenant_id=$1`, testpublication.Tenant).Scan(&mappings); err != nil || mappings != 1 {
		t.Fatalf("creation manufactured an unreviewed product mapping: count=%d err=%v", mappings, err)
	}
}
