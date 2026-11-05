package ordercontract

import "testing"

func TestCuratedFulfillmentFixturesKeepEvidenceAndSchemaScopeExplicit(t *testing.T) {
	t.Parallel()
	manifest, err := ContractManifest()
	if err != nil {
		t.Fatal(err)
	}
	cases, err := CuratedFulfillmentCases()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		_, raw, readErr := CuratedFulfillmentFixture(c.ID)
		if readErr != nil {
			t.Fatal(c.ID, readErr)
		}
		report := manifest.Validate(c.Endpoint, c.Direction, raw)
		if report.Valid != c.ExpectManifestValid {
			t.Fatalf("%s: unexpected contract shape: %+v", c.ID, report)
		}
		if c.Endpoint == "/v3/posting/fbo/list" && (report.Valid || c.EvidenceKind != "THIRD_PARTY_SHAPE_DERIVED_SYNTHETIC") {
			t.Fatal("FBO capture became seller-fulfilled evidence")
		}
	}
}
