package ordercontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func reviewedUnassembledSplit(t *testing.T) fulfillmentExcerptRecord {
	t.Helper()
	raw, err := os.ReadFile("testdata/reviewed/ozon_split_unassembled_2026_11_05.json")
	if err != nil {
		t.Fatal(err)
	}
	var record fulfillmentExcerptRecord
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestUnassembledSplitExcerptPreservesEvidenceLimits(t *testing.T) {
	t.Parallel()
	record := reviewedUnassembledSplit(t)
	raw, err := json.Marshal(record.Excerpt)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != record.ExcerptHash || record.Source.RawHash != nil || record.Coverage != "PARTIAL_SELECTED_FIELDS_NOT_FULL_SCHEMA" {
		t.Fatal("partial excerpt promoted to full schema evidence")
	}
	if record.Review["operation_semantics"] != "SPLIT_WITHOUT_ASSEMBLY" || record.Review["product_id_semantics"] != "SKU" || record.Review["maturity"] != "CONFLICTING_OFFICIAL_METADATA" {
		t.Fatal("distinct operation or official metadata conflict lost")
	}
	for _, key := range []string{"recursive_split_supported_documented", "parent_retirement_proved_by_http200", "specific_error_codes_documented", "permission_for_connected_account_verified", "response_is_ship_result_array"} {
		if record.Review[key] != false {
			t.Fatal("unsupported guarantee invented", key)
		}
	}
}

func TestUnassembledSplitRequestValidatorPreservesRequiredKeysAndInt64(t *testing.T) {
	t.Parallel()
	valid := `{"posting_number":"SYNTHETIC-PARENT","postings":[{"products":[{"product_id":9223372036854775807,"quantity":1}]}]}`
	cases := []struct {
		name, payload string
		valid         bool
	}{
		{"valid", valid, true},
		{"missing posting", strings.Replace(valid, `"posting_number":"SYNTHETIC-PARENT",`, "", 1), false},
		{"missing postings", `{"posting_number":"SYNTHETIC-PARENT"}`, false},
		{"missing products", `{"posting_number":"SYNTHETIC-PARENT","postings":[{}]}`, false},
		{"missing sku", strings.Replace(valid, `"product_id":9223372036854775807,`, "", 1), false},
		{"missing quantity", strings.Replace(valid, `,"quantity":1`, "", 1), false},
		{"int64 overflow", strings.Replace(valid, "9223372036854775807", "9223372036854775808", 1), false},
		{"not ship packages", `{"posting_number":"SYNTHETIC-PARENT","packages":[]}`, false},
		{"no invented minimum", `{"posting_number":"SYNTHETIC-PARENT","postings":[]}`, true},
		{"no invented positive constraint", strings.Replace(valid, `"quantity":1`, `"quantity":0`, 1), true},
	}
	contract := unassembledSplitContract(t, "v1PostingFbsSplitRequest")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := contract.validate([]byte(tc.payload), Report{})
			if r.Valid != tc.valid {
				t.Fatalf("schema validation=%+v", r)
			}
		})
	}
}

func TestUnassembledSplitResponseIsNotShipAcknowledgmentOrRetirementProof(t *testing.T) {
	t.Parallel()
	contract := unassembledSplitContract(t, "v1PostingFbsSplitResponse")
	cases := []struct {
		payload string
		valid   bool
	}{
		{`{"parent_posting":{"posting_number":"SYNTHETIC-PARENT","products":[{"product_id":1,"quantity":2}]},"postings":[{"posting_number":"SYNTHETIC-CHILD","products":[{"product_id":1,"quantity":2}]}]}`, true},
		{`{}`, true}, // Official response has no required list. Business proof remains stricter.
		{`{"result":["SYNTHETIC-CHILD"]}`, false},
		{`{"parent_posting":null}`, false},
		{`{"postings":[{"products":[{"product_id":1}]}]}`, false},
	}
	for _, tc := range cases {
		r := contract.validate([]byte(tc.payload), Report{})
		if r.Valid != tc.valid {
			t.Fatalf("response shape=%+v", r)
		}
	}
}

func unassembledSplitContract(t *testing.T, name string) Contract {
	t.Helper()
	record := reviewedUnassembledSplit(t)
	schemas := record.Excerpt["components"].(map[string]any)["schemas"].(map[string]any)
	return Contract{Endpoint: "/v1/posting/fbs/split", SchemaStatus: "selected_excerpt", Fields: unassembledSchemaFields(schemas, schemas[name].(map[string]any), "$", "required")}
}

// Exercise the existing contract validator directly against the reviewed
// excerpt. This adds no write capability to the production manifest.
func unassembledSchemaFields(schemas map[string]any, schema map[string]any, path, presence string) []Field {
	if reference, ok := schema["$ref"].(string); ok {
		schema = schemas[strings.TrimPrefix(reference, "#/components/schemas/")].(map[string]any)
	}
	kind := schema["type"].(string)
	fields := []Field{{Path: path, Types: []string{kind}, Presence: presence}}
	if kind == "array" {
		return append(fields, unassembledSchemaFields(schemas, schema["items"].(map[string]any), path+"[]", "required")...)
	}
	if kind != "object" {
		return fields
	}
	required := []string{}
	if values, ok := schema["required"].([]any); ok {
		for _, value := range values {
			required = append(required, value.(string))
		}
	}
	for key, value := range schema["properties"].(map[string]any) {
		childPresence := "optional"
		if slices.Contains(required, key) {
			childPresence = "required"
		}
		fields = append(fields, unassembledSchemaFields(schemas, value.(map[string]any), path+"."+key, childPresence)...)
	}
	return fields
}
