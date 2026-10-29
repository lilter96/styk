package catalogsync

import (
	"encoding/json"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestReadbackMatchesOnlySelectedOzonAttributes(t *testing.T) {
	t.Parallel()
	tests := attributeReadbackCases()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := catalog.Command{Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon}},
				Values: map[catalog.Field]catalog.Value{catalog.FieldAttributes: {State: catalog.StateKnown, Data: json.RawMessage(tc.wanted)}}}
			product := catalog.Product{Fields: map[catalog.Field]catalog.Value{catalog.FieldAttributes: {State: catalog.StateKnown, Data: json.RawMessage(tc.actual)}}}
			if matched := ReadbackMatches(command, product); matched != tc.matches {
				t.Fatalf("attribute readback matches=%v, wanted=%v", matched, tc.matches)
			}
		})
	}
}

func attributeReadbackCases() []struct {
	name, wanted, actual string
	matches              bool
} {
	return []struct {
		name, wanted, actual string
		matches              bool
	}{
		{name: "provider generated description allowed", wanted: `[{"id":1,"values":[{"value":"selected"}]}]`, actual: `[{"id":4191,"complex_id":0,"values":[{"dictionary_value_id":0,"value":"generated description"}]},{"id":1,"complex_id":0,"values":[{"dictionary_value_id":0,"value":"selected"}]}]`, matches: true},
		{name: "unordered values", wanted: `[{"id":1,"values":[{"value":"a"},{"value":"b"}]}]`, actual: `[{"id":1,"values":[{"value":"b"},{"value":"a"}]}]`, matches: true},
		{name: "dictionary identity selected", wanted: `[{"id":1,"values":[{"dictionary_value_id":7}]}]`, actual: `[{"id":1,"values":[{"dictionary_value_id":7,"value":"provider label"}]}]`, matches: true},
		{name: "missing selected attribute", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":2,"values":[{"value":"a"}]}]`},
		{name: "changed selected value", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":1,"values":[{"value":"b"}]}]`},
		{name: "extra selected value", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":1,"values":[{"value":"a"},{"value":"b"}]}]`},
		{name: "complex identity changed", wanted: `[{"id":1,"complex_id":2,"values":[{"value":"a"}]}]`, actual: `[{"id":1,"complex_id":3,"values":[{"value":"a"}]}]`},
		{name: "duplicate observed ID", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":1,"values":[{"value":"a"}]},{"id":1,"values":[{"value":"a"}]}]`},
		{name: "duplicate selected ID", wanted: `[{"id":1,"values":[{"value":"a"}]},{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":1,"values":[{"value":"a"}]}]`},
		{name: "conflicting dictionary values", wanted: `[{"id":1,"values":[{"dictionary_value_id":7}]}]`, actual: `[{"id":1,"values":[{"dictionary_value_id":7,"value":"a"},{"dictionary_value_id":7,"value":"b"}]}]`},
		{name: "unknown selected shape", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":1,"unknown":"a","values":[{"value":"a"}]}]`},
		{name: "missing selected values", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":1}]`},
		{name: "conflicting object IDs", wanted: `[{"id":1,"values":[{"value":"a"}]}]`, actual: `[{"id":2,"id":1,"values":[{"value":"a"}]}]`},
	}
}

func TestReadbackRequiresObservableExtensions(t *testing.T) {
	t.Parallel()
	command := catalog.Command{Extensions: map[string]catalog.Value{"vat": catalog.Text("0")}}
	observed := catalog.Product{Extensions: map[string]catalog.Value{"vat": {State: catalog.StateUnknown}}}
	if ReadbackMatches(command, observed) {
		t.Fatal("unobservable VAT was treated as verified")
	}
}

func TestReadbackUsesExactDecimalFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, wanted, actual string
		matches              bool
	}{
		{name: "provider precision", wanted: "25.2", actual: "25.20", matches: true},
		{name: "integer precision", wanted: "0", actual: "0.0000", matches: true},
		{name: "changed amount", wanted: "25.2", actual: "25.21"},
		{name: "leading zeros rejected", wanted: "025.20", actual: "25.20"},
		{name: "exponent rejected", wanted: "25.20", actual: "2.52e1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := catalog.Command{Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon}},
				Values: map[catalog.Field]catalog.Value{catalog.FieldPrice: catalog.Text(tc.wanted)}, Extensions: map[string]catalog.Value{"vat": catalog.Text(tc.wanted)}}
			product := catalog.Product{Fields: map[catalog.Field]catalog.Value{catalog.FieldPrice: catalog.Text(tc.actual)}, Extensions: map[string]catalog.Value{"vat": catalog.Text(tc.actual)}}
			if matched := ReadbackMatches(command, product); matched != tc.matches {
				t.Fatalf("decimal fact matched=%v, expected=%v", matched, tc.matches)
			}
		})
	}
}

func TestNativeOzonAttributesRequireObservedNameAndDescription(t *testing.T) {
	t.Parallel()
	command := catalog.Command{Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon}}, Values: map[catalog.Field]catalog.Value{
		catalog.FieldAttributes: {State: catalog.StateKnown, Data: json.RawMessage(`[{"id":4180,"values":[{"value":"name"}]},{"id":4191,"values":[{"value":"description"}]}]`)}}}
	product := catalog.Product{Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("name"), catalog.FieldDescription: catalog.Text("description"), catalog.FieldAttributes: catalog.Empty()}}
	if !ReadbackMatches(command, product) {
		t.Fatal("observable native fields should verify native attribute requests")
	}
	product.Fields[catalog.FieldDescription] = catalog.Value{State: catalog.StateUnknown}
	if ReadbackMatches(command, product) {
		t.Fatal("missing native description was falsely verified")
	}
}
