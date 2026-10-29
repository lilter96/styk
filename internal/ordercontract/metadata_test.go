package ordercontract

import "testing"

func TestMetadataAcceptsObservedMetaArrayAndDocumentedLegacyArray(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, payload, code string
		valid               bool
	}{
		{name: "legacy array", payload: `{"attributes":[{"type":"string","name":"TEST"}]}`, valid: true},
		{name: "legacy empty array", payload: `{"attributes":[]}`, valid: true},
		{name: "observed metaarray", payload: `{"attributes":{"meta":{"href":"https://example.invalid/attributes","size":0,"limit":1000,"offset":0,"type":"attributemetadata","mediaType":"application/json"}}}`, valid: true},
		{name: "size unknown", payload: `{"attributes":{"meta":{"href":"https://example.invalid/attributes"}}}`, valid: true},
		{name: "nonempty collection", payload: `{"attributes":{"meta":{"href":"https://example.invalid/attributes","size":1}}}`, valid: true},
		{name: "missing collection meta", payload: `{"attributes":{}}`, code: "missing_required"},
		{name: "null collection", payload: `{"attributes":null}`, code: "null_not_allowed"},
		{name: "wrong size type", payload: `{"attributes":{"meta":{"href":"https://example.invalid/attributes","size":"0"}}}`, code: "wrong_type"},
		{name: "new collection key", payload: `{"attributes":{"meta":{"href":"https://example.invalid/attributes"},"customer@example.invalid":"sensitive"}}`, code: "unknown_key"},
	}
	manifest := testManifest(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := manifest.Validate("/entity/customerorder/metadata", "response", []byte(tc.payload))
			if report.Valid != tc.valid || report.SchemaVerified || !hasCode(report, tc.code) {
				t.Fatalf("unexpected metadata report: %+v", report)
			}
		})
	}
}
