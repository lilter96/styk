package ordercontract

import "testing"

func TestNativeCompositionRetainedPositionUUIDContract(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	for _, tc := range []struct {
		name, payload string
		valid         bool
	}{
		{"retained", `{"positions":[{"id":"00000000-0000-4000-8000-000000000099","quantity":1,"price":100,"reserve":0,"discount":0,"vat":0,"vatEnabled":false}]}`, true},
		{"invalid_id_type", `{"positions":[{"id":99,"quantity":1}]}`, false},
		{"no_readonly_shipped", `{"positions":[{"id":"native","shipped":0}]}`, false},
		{"no_implicit_new_field", `{"positions":[{"id":"native","newProviderMeaning":0}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := manifest.Validate("/entity/customerorder/{id}", "request", []byte(tc.payload))
			if report.Valid != tc.valid || !report.SchemaVerified {
				t.Fatalf("report=%+v", report)
			}
		})
	}
}
