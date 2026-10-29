package moysklad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogMetadataUsesOfficialEnvelopesAndIdentifierNamespaces(t *testing.T) {
	t.Parallel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("metadata discovery attempted a mutation")
		}
		catalogServeMetadata(t, w, r)
	}))
	defer s.Close()
	c, err := NewCatalogClient(catalogBaseClient(t, s.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := c.ReadCatalogMetadata(context.Background(), catalogTestScope(), catalogTestAccountRef())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Account != catalogTestAccountRef() || len(metadata.Folders) != 1 || len(metadata.Units) != 1 || len(metadata.Attributes) != 1 || len(metadata.Characteristics) != 1 || len(metadata.PriceTypes) != 1 {
		t.Fatalf("incomplete provider metadata: %+v", metadata)
	}
	if metadata.Folders[0].ID != catalogTestFolder || metadata.Units[0].Code != "796" || metadata.Attributes[0].Type != "string" || !metadata.Attributes[0].Required || metadata.PriceTypes[0].ID != catalogTestPrice || len(metadata.BarcodeFormats) != 5 {
		t.Fatalf("provider metadata facts were transformed incorrectly: %+v", metadata)
	}
}

func catalogServeMetadata(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	base := "http://" + r.Host
	var response any
	switch r.URL.Path {
	case "/context/employee":
		response = map[string]any{"accountId": catalogTestAccount}
	case "/entity/productfolder":
		response = catalogMetadataPage(t, r, map[string]any{"id": catalogTestFolder, "accountId": catalogTestAccount, "name": "Folder", "code": "F", "meta": catalogMetadataRef(base+r.URL.Path+"/"+catalogTestFolder, "productfolder")})
	case "/entity/uom":
		response = catalogMetadataPage(t, r, map[string]any{"id": catalogTestUnit, "name": "Piece", "code": "796", "meta": catalogMetadataRef(base+r.URL.Path+"/"+catalogTestUnit, "uom")})
	case "/entity/product/metadata/attributes":
		response = catalogMetadataPage(t, r, map[string]any{"id": catalogTestAttr, "name": "Material", "type": "string", "required": true, "meta": catalogMetadataRef(base+r.URL.Path+"/"+catalogTestAttr, "attributemetadata")})
	case "/entity/variant/metadata":
		response = map[string]any{"characteristics": []any{map[string]any{"id": catalogTestAttr, "name": "Size", "type": "string", "required": false}}}
	case "/context/companysettings/pricetype":
		response = []any{map[string]any{"id": catalogTestPrice, "name": "Retail", "externalCode": "retail", "meta": catalogMetadataRef(base+r.URL.Path+"/"+catalogTestPrice, "pricetype")}}
	default:
		t.Errorf("undocumented metadata path %s", r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
}

func catalogMetadataRef(href, kind string) map[string]any {
	return map[string]any{"href": href, "type": kind, "mediaType": "application/json"}
}

func catalogMetadataPage(t *testing.T, r *http.Request, row map[string]any) map[string]any {
	t.Helper()
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		t.Error(err)
		return nil
	}
	return map[string]any{"meta": map[string]any{"size": 1, "offset": 0, "limit": limit}, "rows": []any{row}}
}

func TestCatalogCreateAcceptsBoundedProviderMetadataValues(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, cmd := s.writer(t), catalogCreateCommand("product")
	cmd.Values[catalog.FieldCategory] = catalogJSONValue(t, s.reference("productfolder", catalogTestFolder))
	cmd.Values[catalog.FieldUnit] = catalogJSONValue(t, s.reference("uom", catalogTestUnit))
	cmd.Values[catalog.FieldPrice] = catalogJSONValue(t, []any{map[string]any{"value": 19900, "currency": s.reference("currency", catalogTestMoney), "priceType": s.priceReference()}})
	cmd.Values[catalog.FieldBarcodes] = catalogJSONValue(t, []any{map[string]any{"code128": "STYK-CATALOG-1"}})
	cmd.Values[catalog.FieldAttributes] = catalogJSONValue(t, []any{map[string]any{"meta": catalogMetadataRef(s.server.URL+"/entity/product/metadata/attributes/"+catalogTestAttr, "attributemetadata"), "value": "Cotton"}})
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied {
		t.Fatalf("valid provider catalog metadata was not applied: %+v %v", r, err)
	}
	p := catalogReadOne(t, c, "product", r.ExternalID)
	for _, field := range []catalog.Field{catalog.FieldCategory, catalog.FieldUnit, catalog.FieldPrice, catalog.FieldBarcodes, catalog.FieldAttributes} {
		if !catalog.EqualValue(p.Fields[field], cmd.Values[field]) {
			t.Errorf("provider metadata field %s changed: %+v", field, p.Fields[field])
		}
	}
}

func catalogJSONValue(t *testing.T, value any) catalog.Value {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(fmt.Errorf("marshal catalog fixture: %w", err))
	}
	return catalog.Value{State: catalog.StateKnown, Data: raw}
}
