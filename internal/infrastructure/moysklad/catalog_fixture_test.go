package moysklad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

const (
	catalogTestAccount = "10000000-0000-0000-0000-000000000001"
	catalogTestProduct = "10000000-0000-0000-0000-000000000002"
	catalogTestVariant = "10000000-0000-0000-0000-000000000003"
	catalogTestCreate  = "10000000-0000-0000-0000-000000000004"
	catalogTestFolder  = "10000000-0000-0000-0000-000000000005"
	catalogTestUnit    = "10000000-0000-0000-0000-000000000006"
	catalogTestAttr    = "10000000-0000-0000-0000-000000000007"
	catalogTestPrice   = "10000000-0000-0000-0000-000000000008"
	catalogTestMoney   = "10000000-0000-0000-0000-000000000009"
)

type catalogSimulator struct {
	t       *testing.T
	server  *httptest.Server
	client  *Client
	mu      sync.Mutex
	account string
	rows    map[string]map[string]any
	writes  int
	mode    string
	bodies  []map[string]any
	events  []string
}

func catalogTestScope() catalog.Scope {
	return catalog.Scope{Tenant: "catalog-test", MoyskladAccount: catalogTestAccount, OzonAccount: "77"}
}

func catalogTestAccountRef() catalog.Account {
	return catalog.Account{Provider: catalog.ProviderMoySklad, ID: catalogTestAccount}
}

func newCatalogSimulator(t *testing.T) *catalogSimulator {
	t.Helper()
	s := &catalogSimulator{t: t, account: catalogTestAccount, rows: make(map[string]map[string]any)}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.server.Close)
	client, err := New(Options{BaseURL: s.server.URL + "/", Token: "test-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 3, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	s.client = client
	s.add("product", catalogTestProduct, "Parent")
	return s
}

func (s *catalogSimulator) writer(t *testing.T) *CatalogClient {
	t.Helper()
	c, err := NewControlledTestCatalogClient(s.client, catalogTestScope())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (s *catalogSimulator) reference(kind, id string) map[string]any {
	return map[string]any{"meta": map[string]any{"href": s.server.URL + "/entity/" + kind + "/" + id, "type": kind, "mediaType": "application/json"}}
}

func (s *catalogSimulator) add(kind, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.reference(kind, id)
	row["id"], row["accountId"], row["name"] = id, s.account, name
	row["updated"], row["archived"], row["externalCode"] = "2026-10-29 10:00:00.001", false, "existing-"+id
	row["description"], row["article"], row["code"] = "Original description", "article-1", "code-1"
	row["barcodes"] = []any{map[string]any{"code128": "KEEP-ME"}}
	row["uom"] = s.reference("uom", catalogTestUnit)
	row["productFolder"] = s.reference("productfolder", catalogTestFolder)
	row["buyPrice"] = map[string]any{"value": 123.5}
	row["salePrices"] = []any{map[string]any{"value": 1500, "currency": s.reference("currency", catalogTestMoney), "priceType": s.priceReference()}}
	row["attributes"] = []any{map[string]any{"id": catalogTestAttr, "name": "Keep attribute", "value": "unchanged"}}
	if kind == "variant" {
		row["product"] = s.reference("product", catalogTestProduct)
		row["characteristics"] = []any{map[string]any{"id": catalogTestAttr, "name": "Size", "value": "M"}}
	}
	s.rows[kind+":"+id] = row
}

func (s *catalogSimulator) priceReference() map[string]any {
	return map[string]any{"meta": map[string]any{"href": s.server.URL + "/context/companysettings/pricetype/" + catalogTestPrice, "type": "pricetype", "mediaType": "application/json"}}
}

func (s *catalogSimulator) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		s.t.Error("catalog request did not use the existing bearer transport")
	}
	s.events = append(s.events, r.Method+" "+r.URL.Path)
	if r.Method == http.MethodGet {
		s.serveRead(w, r)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		s.t.Errorf("unexpected catalog mutation method %s", r.Method)
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		return
	}
	s.serveWrite(w, r)
}

func (s *catalogSimulator) serveRead(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/context/employee" {
		s.json(w, map[string]any{"accountId": s.account})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == "entity" {
		row, ok := s.rows[parts[1]+":"+parts[2]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.json(w, row)
		return
	}
	if len(parts) != 2 || parts[0] != "entity" || (parts[1] != "product" && parts[1] != "variant") {
		s.t.Errorf("unexpected catalog GET %s", r.URL.Path)
		http.NotFound(w, r)
		return
	}
	s.serveCollection(w, r, parts[1])
}

func (s *catalogSimulator) serveCollection(w http.ResponseWriter, r *http.Request, kind string) {
	limit, limitErr := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, offsetErr := strconv.Atoi(r.URL.Query().Get("offset"))
	if limitErr != nil || offsetErr != nil || limit < 1 || offset < 0 {
		s.t.Error("invalid simulator pagination")
		http.Error(w, "pagination", http.StatusBadRequest)
		return
	}
	keys := make([]string, 0, len(s.rows))
	for key, row := range s.rows {
		marker := strings.TrimPrefix(strings.SplitN(r.URL.Query().Get("filter"), ";", 2)[0], "externalCode=")
		if strings.HasPrefix(key, kind+":") && (marker == "" || row["externalCode"] == marker) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	rows := make([]map[string]any, 0, min(limit, len(keys)))
	for i := offset; i < len(keys) && i < offset+limit; i++ {
		rows = append(rows, s.rows[keys[i]])
	}
	s.json(w, map[string]any{"meta": map[string]any{"size": len(keys), "limit": limit, "offset": offset}, "rows": rows})
}

func (s *catalogSimulator) serveWrite(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "entity" || (parts[1] != "product" && parts[1] != "variant") {
		s.t.Error("catalog adapter reached a forbidden mutation path")
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.t.Error(err)
		http.Error(w, "decode", http.StatusBadRequest)
		return
	}
	s.writes++
	s.bodies = append(s.bodies, body)
	id := catalogTestCreate
	if len(parts) == 3 {
		id = parts[2]
	}
	row := s.rows[parts[1]+":"+id]
	if row == nil {
		row = s.reference(parts[1], id)
		row["id"], row["accountId"], row["archived"] = id, s.account, false
	}
	for key, value := range body {
		row[key] = value
	}
	if row["name"] == nil {
		row["name"] = "Parent (M)"
	}
	row["updated"] = fmt.Sprintf("2026-10-29 11:00:00.%03d", s.writes)
	s.enrichMetadata(row)
	s.rows[parts[1]+":"+id] = row
	if s.mode == "disconnect" {
		s.disconnect(w)
		return
	}
	s.writeOutcome(w, row)
}

func (s *catalogSimulator) writeOutcome(w http.ResponseWriter, row map[string]any) {
	switch s.mode {
	case "500", "429":
		status, err := strconv.Atoi(s.mode)
		if err != nil {
			s.t.Error(err)
			return
		}
		http.Error(w, "ambiguous provider response", status)
	case "malformed":
		_, err := w.Write([]byte(`{"ok":true}`))
		if err != nil {
			s.t.Error(err)
		}
	case "oversized":
		_, err := w.Write([]byte(strings.Repeat("x", catalogResponseLimit+1)))
		if err != nil {
			return // The bounded reader may close while the oversized body is sent.
		}
	case "redirect":
		w.Header().Set("Location", s.server.URL+"/entity/product")
		w.WriteHeader(http.StatusTemporaryRedirect)
	case "wrong-account", "wrong-id":
		copyRow := make(map[string]any, len(row))
		for key, value := range row {
			copyRow[key] = value
		}
		if s.mode == "wrong-account" {
			copyRow["accountId"] = catalogTestProduct
		} else {
			copyRow["id"] = catalogTestProduct
		}
		s.json(w, copyRow)
	default:
		s.json(w, row)
	}
}

func (s *catalogSimulator) disconnect(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		s.t.Error("loopback server cannot simulate interrupted response")
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		s.t.Error(err)
		return
	}
	if err := conn.Close(); err != nil {
		s.t.Error(err)
	}
}

func (s *catalogSimulator) enrichMetadata(row map[string]any) {
	if s.mode != "curated-metadata" {
		return
	}
	for _, field := range []string{"characteristics", "salePrices", "barcodes", "attributes"} {
		items, ok := row[field].([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			s.enrichMetadataItem(field, item)
		}
		for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
			items[left], items[right] = items[right], items[left]
		}
	}
	for _, field := range []string{"productFolder", "uom"} {
		if reference, ok := row[field].(map[string]any); ok {
			reference["name"] = "Provider display name"
		}
	}
}

func (s *catalogSimulator) enrichMetadataItem(field string, value any) {
	item, ok := value.(map[string]any)
	if !ok {
		return
	}
	switch field {
	case "characteristics":
		item["name"] = "Provider characteristic display name"
		item["meta"] = catalogMetadataRef(s.server.URL+"/entity/variant/metadata/characteristics/"+item["id"].(string), "attributemetadata")
	case "salePrices":
		item["value"] = json.Number("19900.00")
		for _, key := range []string{"currency", "priceType"} {
			if reference, ok := item[key].(map[string]any); ok {
				reference["name"] = "Expanded provider name"
				reference["id"] = "Read only provider identity"
			}
		}
	case "attributes":
		item["name"], item["id"], item["type"] = "Provider attribute name", catalogTestAttr, "string"
	}
}

func (s *catalogSimulator) json(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.t.Error(err)
	}
}

func (s *catalogSimulator) state() (int, []map[string]any, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes, append([]map[string]any(nil), s.bodies...), append([]string(nil), s.events...)
}

func (s *catalogSimulator) unownedJSON(t *testing.T, kind, id string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any)
	for key, value := range s.rows[kind+":"+id] {
		if key != "name" && key != "description" && key != "updated" {
			out[key] = value
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func catalogCreateCommand(kind string) catalog.Command {
	return catalog.Command{ID: "durable-command-1", Scope: catalogTestScope(), MappingID: "mapping-1", MappingVersion: 1, PolicyRevision: "policy-1", Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: "77"}, EntityType: "product", ID: "42", OfferID: "offer-42"}, Target: catalog.Identity{Account: catalogTestAccountRef(), EntityType: kind}, Action: catalog.ActionCreate, CreateID: catalogTestCreate, CreateExternalCode: "styk-catalog-durable-1", Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Created item"), catalog.FieldDescription: catalog.Text("Created description")}}
}

func catalogReadOne(t *testing.T, c *CatalogClient, kind, id string) catalog.Product {
	t.Helper()
	request := catalog.ReadRequest{Scope: catalogTestScope(), Account: catalogTestAccountRef(), Identities: []catalog.Identity{{Account: catalogTestAccountRef(), EntityType: kind, ID: id}}}
	snap, err := c.ReadCatalog(context.Background(), request)
	if err != nil || len(snap.Products) != 1 {
		t.Fatalf("read catalog target: snapshot=%+v error=%v", snap, err)
	}
	return snap.Products[0]
}

func catalogUpdateCommand(product catalog.Product) catalog.Command {
	cmd := catalogCreateCommand(product.Identity.EntityType)
	cmd.Target, cmd.Action = product.Identity, catalog.ActionUpdate
	cmd.CreateID, cmd.CreateExternalCode = "", ""
	cmd.ExpectedTargetRevision = product.Revision
	return cmd
}

func catalogBind(t *testing.T, c *CatalogClient, cmd catalog.Command, authorize func(context.Context) (bool, error)) {
	t.Helper()
	if err := c.BindCatalogIntent(cmd, authorize); err != nil {
		t.Fatal(err)
	}
}

func catalogAuthorize(context.Context) (bool, error) { return true, nil }
