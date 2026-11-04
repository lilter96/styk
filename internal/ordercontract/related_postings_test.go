package ordercontract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func relatedPostingPayload(t *testing.T, direction string, edit func(map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/ozon-get-" + direction + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	edit(payload)
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRelatedPostingGETRequestBooleanRemainsOptionalAndPartial(t *testing.T) {
	t.Parallel()
	for _, value := range []any{true, false, nil, "true", 1} {
		t.Run("request_flag", func(t *testing.T) {
			t.Parallel()
			manifest := testManifest(t)
			raw := relatedPostingPayload(t, "request", func(p map[string]any) { p["with"].(map[string]any)["related_postings"] = value })
			_, valid := value.(bool)
			report := manifest.Validate("/v3/posting/fbs/get", "request", raw)
			if report.Valid != valid || report.SchemaVerified {
				t.Fatalf("GET flag type or evidence scope changed: %+v", report)
			}
		})
	}
}

func TestRelatedPostingResponseNullAndArrayTypesNeverAssertInventoryLineage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value any
		valid bool
	}{
		{"official null example", nil, true},
		{"empty object", map[string]any{}, true},
		{"empty list", map[string]any{"related_posting_numbers": []any{}}, true},
		{"posting references", map[string]any{"related_posting_numbers": []any{"SYNTHETIC-ASSEMBLY-SIBLING"}}, true},
		{"wrong parent type", []any{}, false},
		{"unknown list", map[string]any{"related_posting_numbers": nil}, false},
		{"wrong item type", map[string]any{"related_posting_numbers": []any{9007199254740993}}, false},
		{"wrong list type", map[string]any{"related_posting_numbers": "SYNTHETIC"}, false},
		{"drift", map[string]any{"future_parent_retired": true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := relatedPostingPayload(t, "response", func(p map[string]any) { p["result"].(map[string]any)["related_postings"] = tc.value })
			report := testManifest(t).Validate("/v3/posting/fbs/get", "response", raw)
			if report.Valid != tc.valid || report.SchemaVerified {
				t.Fatalf("related posting shape/evidence changed: %+v", report)
			}
		})
	}
}

func TestRelatedPostingManifestKeepsReferencesStructuredReadOnlyAndGETOnly(t *testing.T) {
	t.Parallel()
	found := 0
	for _, contract := range testManifest(t).Contracts {
		for _, field := range contract.Fields {
			if field.Path != "$.with.related_postings" && field.Path != "$.result.related_postings" && field.Path != "$.result.related_postings.related_posting_numbers" && field.Path != "$.result.related_postings.related_posting_numbers[]" {
				continue
			}
			found++
			verification := "official_field_verified"
			if field.Path == "$.result.related_postings.related_posting_numbers" {
				verification = "official_items_verified_runtime_array_validation"
			}
			if contract.Endpoint != "/v3/posting/fbs/get" || contract.SchemaStatus != "partial_unverified" || field.Classification != "structured" || field.ReadOnly != (contract.Direction == "response") || field.Verification != verification {
				t.Fatal("assembly references changed authority or unrelated endpoint contracts")
			}
		}
	}
	if found != 4 {
		t.Fatal("all four related GET field contracts required")
	}
}
