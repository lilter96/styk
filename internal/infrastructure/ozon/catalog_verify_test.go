package ozon

import (
	"encoding/json"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func catalogVerificationFixture() (*CatalogClient, catalog.Command, catalog.Product) {
	client := &CatalogClient{scope: catalogFixtureScope, read: &Client{id: "1"}}
	command := catalogFixtureCommand()
	command.Values = map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("name"), catalog.FieldPrice: catalog.Text("25.2000"), catalog.FieldAttributes: catalogRaw([]CatalogAttribute{{ID: 123, Values: []AttributeValue{{DictionaryValueID: 10, Value: "label at approval"}}}})}
	command.Extensions = map[string]catalog.Value{"vat": catalog.Text("0.20")}
	product := catalog.Product{Identity: catalogFixtureIdentity(), Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("name"), catalog.FieldPrice: catalog.Text("25.2"), catalog.FieldAttributes: catalogRaw([]CatalogAttribute{{ID: 999, Values: []AttributeValue{{Value: "provider default"}}}, {ID: 123, Values: []AttributeValue{{DictionaryValueID: 10, Value: "provider label"}}}})}, Extensions: map[string]catalog.Value{"vat": catalog.Text("0.2")}}
	return client, command, product
}

func TestCatalogReadbackVerifiesOwnedAttributesAndDecimalFacts(t *testing.T) {
	t.Parallel()
	client, command, product := catalogVerificationFixture()
	problems, err := client.VerifyCatalogFields(command, product)
	if err != nil || len(problems) != 0 {
		t.Fatalf("verified selected facts rejected: %+v %v", problems, err)
	}
	product.Fields[catalog.FieldAttributes] = catalogRaw([]CatalogAttribute{{ID: 123, Values: []AttributeValue{{DictionaryValueID: 11, Value: "different actual dictionary value"}}}})
	problems, err = client.VerifyCatalogFields(command, product)
	if err != nil || len(problems) != 1 || problems[0].Field != catalog.FieldAttributes {
		t.Fatalf("changed dictionary value accepted: %+v %v", problems, err)
	}
}

func TestCatalogReadbackCannotVerifyUnavailableVATOrRewrittenImages(t *testing.T) {
	t.Parallel()
	client, command, product := catalogVerificationFixture()
	product.Extensions["vat"] = catalog.Value{State: catalog.StateUnknown}
	command.Values[catalog.FieldImages] = catalogRaw([]string{"https://external.example/item.png"})
	product.Fields[catalog.FieldImages] = catalogRaw([]string{"https://ir.ozone.ru/new-cdn-image.png"})
	problems, err := client.VerifyCatalogFields(command, product)
	if err != nil || len(problems) != 2 {
		t.Fatalf("unobserved facts verified: %+v %v", problems, err)
	}
	if problems[0].Code != "VAT_READBACK_UNAVAILABLE" || problems[1].Code != "IMAGE_REFERENCES_NOT_VERIFIED" {
		t.Fatalf("uncertainty lost: %+v", problems)
	}
}

func TestCatalogCanonicalProjectionRetainsUnownedFactsOnce(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"id":4180,"values":[{"value":"name"}]},{"id":4191,"values":[{"value":"description"}]},{"id":123,"unknown_provider_key":true,"values":[{"value":"kept"}]}]`)
	value := catalogUnownedAttributes(raw)
	var attributes []map[string]json.RawMessage
	if json.Unmarshal(value.Data, &attributes) != nil || len(attributes) != 1 || string(attributes[0]["unknown_provider_key"]) != "true" {
		t.Fatalf("unowned attribute facts lost: %+v", value)
	}
	extra := catalogInfoExtras(catalogWire{"name": json.RawMessage(`"name"`), "updated_at": json.RawMessage(`"timestamp"`), "unowned": json.RawMessage(`true`)})
	var fields catalogWire
	if json.Unmarshal(extra.Data, &fields) != nil || len(fields) != 1 || string(fields["unowned"]) != "true" {
		t.Fatalf("duplicated owned or lost unowned facts: %+v", extra)
	}
}
