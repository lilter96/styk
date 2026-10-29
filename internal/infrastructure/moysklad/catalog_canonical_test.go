package moysklad

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogCanonicalCharacteristicsMatchApprovedFactsOnEngineReadback(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.mode = "curated-metadata"
	c, cmd := s.writer(t), catalogCreateCommand("variant")
	cmd.Parent = &catalog.Identity{Account: catalogTestAccountRef(), EntityType: "product", ID: catalogTestProduct}
	delete(cmd.Values, catalog.FieldName)
	cmd.Extensions = map[string]catalog.Value{"moysklad.characteristics": catalogJSONValue(t, []any{map[string]any{"id": catalogTestAttr, "value": "M"}, map[string]any{"id": catalogTestPrice, "value": "Red"}})}
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied {
		t.Fatalf("provider-rich variant was not verified: %+v %v", r, err)
	}
	p := catalogReadOne(t, c, "variant", r.ExternalID)
	if !catalog.EqualValue(cmd.Extensions["moysklad.characteristics"], p.Extensions["moysklad.characteristics"]) {
		t.Fatalf("engine-style extension comparison failed: approved=%s readback=%s", cmd.Extensions["moysklad.characteristics"].Data, p.Extensions["moysklad.characteristics"].Data)
	}
	metadata := p.Extensions["moysklad.characteristic_metadata"]
	if metadata.State != catalog.StateKnown || !strings.Contains(string(metadata.Data), `"name"`) || !strings.Contains(string(metadata.Data), `"meta"`) || metadata.Provenance.Kind != catalog.EvidenceAPICurated {
		t.Fatal("provider display metadata was discarded or misrepresented")
	}
}

func TestCatalogCanonicalMetadataMatchesAfterProviderExpansionAndReordering(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.mode = "curated-metadata"
	c, cmd := s.writer(t), catalogCreateCommand("product")
	cmd.Values[catalog.FieldCategory] = catalogJSONValue(t, s.reference("productfolder", catalogTestFolder))
	cmd.Values[catalog.FieldUnit] = catalogJSONValue(t, s.reference("uom", catalogTestUnit))
	cmd.Values[catalog.FieldPrice] = catalogJSONValue(t, []any{map[string]any{"value": 19900, "currency": s.reference("currency", catalogTestMoney), "priceType": s.priceReference()}})
	cmd.Values[catalog.FieldBarcodes] = catalogJSONValue(t, []any{map[string]any{"code128": "STYK-CATALOG-1"}, map[string]any{"ean13": "2000000000000"}})
	cmd.Values[catalog.FieldAttributes] = catalogJSONValue(t, []any{map[string]any{"meta": catalogMetadataRef(s.server.URL+"/entity/product/metadata/attributes/"+catalogTestAttr, "attributemetadata"), "value": "Cotton"}})
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied {
		t.Fatalf("provider metadata write was not verified: %+v %v", r, err)
	}
	p := catalogReadOne(t, c, "product", r.ExternalID)
	for field, approved := range cmd.Values {
		if !catalog.EqualValue(approved, p.Fields[field]) {
			t.Errorf("engine-style selected %s comparison failed: approved=%s readback=%s", field, approved.Data, p.Fields[field].Data)
		}
	}
}

func TestCatalogCharacteristicsRejectUnreviewableOrNoncanonicalSelections(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`[{"name":"Inferred by name","value":"M"}]`,
		`[{"id":"` + catalogTestAttr + `","name":"Display name","value":"M"}]`,
		`[{"id":"` + catalogTestPrice + `","value":"Red"},{"id":"` + catalogTestAttr + `","value":"M"}]`,
		`[{"id":"` + catalogTestAttr + `","value":"M"},{"id":"` + catalogTestAttr + `","value":"L"}]`,
	} {
		s := newCatalogSimulator(t)
		cmd := catalogCreateCommand("variant")
		cmd.Parent = &catalog.Identity{Account: catalogTestAccountRef(), EntityType: "product", ID: catalogTestProduct}
		cmd.Extensions = map[string]catalog.Value{"moysklad.characteristics": {State: catalog.StateKnown, Data: json.RawMessage(raw)}}
		if err := s.writer(t).BindCatalogIntent(cmd, catalogAuthorize); err == nil {
			t.Fatalf("noncanonical or fabricated characteristic selection admitted: %s", raw)
		}
		_, _, events := s.state()
		if len(events) != 0 {
			t.Fatal("invalid selection reached the provider")
		}
	}
}

func TestCatalogComplexAttributeFactsRemainUnknownForUnsupportedWrites(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.mu.Lock()
	s.rows["product:"+catalogTestProduct]["attributes"] = []any{map[string]any{"meta": catalogMetadataRef(s.server.URL+"/entity/product/metadata/attributes/"+catalogTestAttr, "attributemetadata"), "value": s.reference("product", catalogTestProduct)}}
	s.mu.Unlock()
	c := s.writer(t)
	p := catalogReadOne(t, c, "product", catalogTestProduct)
	if p.Fields[catalog.FieldAttributes].State != catalog.StateUnknown || p.Extensions["moysklad.attribute_metadata"].State != catalog.StateKnown {
		t.Fatal("an unsupported reference attribute was exposed as a writable scalar fact")
	}
	cmd := catalogCreateCommand("product")
	cmd.Values[catalog.FieldAttributes] = p.Extensions["moysklad.attribute_metadata"]
	if err := c.BindCatalogIntent(cmd, catalogAuthorize); err == nil {
		t.Fatal("unsupported reference attribute write was admitted")
	}
}
