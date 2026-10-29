package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func catalogFixtureCreate() catalog.Command {
	command := catalogFixtureCommand()
	command.Action, command.Target.ID, command.Target.SKU = catalog.ActionCreate, "", ""
	command.IdempotencyKey = "durable-command-key"
	command.Values = map[catalog.Field]catalog.Value{
		catalog.FieldName: catalog.Text("reviewed name"), catalog.FieldDescription: catalog.Text("reviewed description"),
		catalog.FieldPrice: catalog.Text("25.20"), catalog.FieldCurrency: catalog.Text("RUB"),
		catalog.FieldCategory:   catalogRaw(CategorySelection{DescriptionCategoryID: 7, TypeID: 8}),
		catalog.FieldDimensions: catalogRaw(Dimensions{Height: 1, Depth: 2, Width: 3, DimensionUnit: "mm", Weight: 4, WeightUnit: "g"}),
		catalog.FieldAttributes: catalogRaw([]CatalogAttribute{{ID: 123, Values: []AttributeValue{{DictionaryValueID: 10, Value: "Chosen"}}}}),
	}
	command.Extensions = map[string]catalog.Value{"vat": catalog.Text("0.22")}
	return command
}

func catalogCreationReads(t *testing.T, w http.ResponseWriter, r *http.Request, disabled bool, readMask *atomic.Int32) bool {
	t.Helper()
	switch r.URL.Path {
	case productInfoPath:
		readMask.Or(1)
		replyCatalogJSON(t, w, map[string]any{"items": []any{}})
	case catalogTreePath:
		readMask.Or(2)
		replyCatalogJSON(t, w, map[string]any{"result": []CategoryNode{{DescriptionCategoryID: 7, Disabled: disabled, Children: []CategoryNode{{TypeID: 8, Children: []CategoryNode{}}}}}})
	case catalogSchemaPath:
		readMask.Or(4)
		replyCatalogJSON(t, w, map[string]any{"result": []CategoryAttribute{{ID: 123, Required: true, DictionaryID: 99}, {ID: 4191, Type: "multiline"}}})
	case catalogDictionaryPath:
		readMask.Or(8)
		var body struct {
			AttributeID int64 `json:"attribute_id"`
			LastValueID int64 `json:"last_value_id"`
			Limit       int   `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return true
		}
		if body.AttributeID != 123 || body.LastValueID != 9 || body.Limit != 1 {
			t.Errorf("dictionary membership not checked exactly: %+v", body)
		}
		replyCatalogJSON(t, w, map[string]any{"result": []DictionaryValue{{ID: 10, Value: "Chosen"}}, "has_next": false})
	default:
		return false
	}
	return true
}

func TestCatalogCreationRequiresExplicitFactsAndCurrentProviderSchema(t *testing.T) {
	t.Parallel()
	var reads, writes atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if catalogCreationReads(t, w, r, false, &reads) {
			return
		}
		if r.URL.Path != catalogImportPath {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		writes.Add(1)
		var body struct {
			Items []catalogCreateItem `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Items) != 1 || body.Items[0].VAT != "0.22" || body.Items[0].Height != 1 || body.Items[0].TypeID != 8 || len(body.Items[0].Attributes) != 2 {
			t.Errorf("reviewed create facts changed: %+v", body)
		}
		replyCatalogJSON(t, w, map[string]any{"result": map[string]any{"task_id": 92}})
	})
	command := catalogFixtureCreate()
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return reads.Load() == 15, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteCatalog(context.Background(), command)
	if err != nil || receipt.State != catalog.ReceiptAccepted || receipt.TaskID != "92" || writes.Load() != 1 {
		t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, writes.Load())
	}
}

func TestCatalogCreationNeverInventsMissingOrIncompatibleFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*catalog.Command)
		disabled bool
	}{
		{"missing VAT", func(c *catalog.Command) { delete(c.Extensions, "vat") }, false},
		{"missing dimensions", func(c *catalog.Command) { delete(c.Values, catalog.FieldDimensions) }, false},
		{"required attribute missing", func(c *catalog.Command) { c.Values[catalog.FieldAttributes] = catalogRaw([]CatalogAttribute{}) }, false},
		{"dictionary value mismatch", func(c *catalog.Command) {
			c.Values[catalog.FieldAttributes] = catalogRaw([]CatalogAttribute{{ID: 123, Values: []AttributeValue{{DictionaryValueID: 10, Value: "invented"}}}})
		}, false},
		{"disabled category", func(*catalog.Command) {}, true},
		{"private image", func(c *catalog.Command) {
			c.Values[catalog.FieldImages] = catalogRaw([]string{"https://127.0.0.1/private.jpg"})
		}, false},
		{"unknown dimensions member", func(c *catalog.Command) {
			c.Values[catalog.FieldDimensions] = catalog.Value{State: catalog.StateKnown, Data: json.RawMessage(`{"height":1,"depth":1,"width":1,"dimension_unit":"mm","weight":1,"weight_unit":"g","guessed":true}`)}
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertCatalogCreationRejected(t, tc.mutate, tc.disabled)
		})
	}
}

func assertCatalogCreationRejected(t *testing.T, mutate func(*catalog.Command), disabled bool) {
	t.Helper()
	t.Parallel()
	var reads, writes atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !catalogCreationReads(t, w, r, disabled, &reads) {
			writes.Add(1)
		}
	})
	command := catalogFixtureCreate()
	mutate(&command)
	if err := client.BindCatalogIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if receipt, err := client.WriteCatalog(context.Background(), command); err == nil || receipt.State != catalog.ReceiptRejected || len(receipt.Errors) == 0 {
		t.Fatalf("invalid create accepted: %+v %v", receipt, err)
	}
	if writes.Load() != 0 {
		t.Fatal("invalid create reached provider writer")
	}
}
