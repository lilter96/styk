package ordercontract

import (
	"strings"
	"testing"
)

func TestCoverageIncludesEveryManifestFieldWithoutPromotingFixtures(t *testing.T) {
	t.Parallel()
	report, err := CoverageReport()
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(t)
	expected, implemented := 0, 0
	for _, contract := range manifest.Contracts {
		expected += len(contract.Fields)
	}
	for _, row := range report.Rows {
		if row.Owner == "" || row.Transformation == "" || row.Reason == "" || len(row.Prerequisites) == 0 || !row.Proof.ValidatorFixtureCovered {
			t.Fatal("field lacks explicit handling or independent fixture coverage")
		}
		if row.Proof.Implemented {
			implemented++
		}
		if row.IntegrationReason == "" || row.Proof.DeterministicIntegration != reviewedRelatedIntegration(row) {
			t.Fatal("fixture coverage was promoted into unaudited integration coverage")
		}
	}
	if len(report.Rows) != expected || implemented == 0 || implemented >= expected/2 {
		t.Fatalf("unexpected coverage counts: rows=%d expected=%d implemented=%d", len(report.Rows), expected, implemented)
	}
}

func TestCoverageUnsupportedAndFulfillmentWritesStayUnproven(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	for _, row := range manifest.FieldCoverage() {
		if row.Fate == FateUnsupported && (row.Proof.Implemented || row.Proof.LiveRead || row.Proof.LiveWrite) {
			t.Fatalf("unsupported field gained implementation evidence: %s %s", row.Endpoint, row.Path)
		}
		if strings.HasPrefix(row.Endpoint, "/v") && row.Proof.LiveWrite {
			t.Fatal("Ozon fulfillment or rFBS write evidence was invented")
		}
		if row.Endpoint == "/entity/customerorder/{id}" && row.Direction == "request" && row.Proof.LiveWrite && !strings.HasPrefix(row.Path, "$.attributes[].") {
			t.Fatal("unverified native update gained live write evidence")
		}
	}
}

func TestCoverageKeepsNativeMoneyAndProviderAggregatesDistinct(t *testing.T) {
	t.Parallel()
	rows := testManifest(t).FieldCoverage()
	tests := []struct {
		endpoint, direction, path string
		fate                      FieldFate
		liveRead, liveWrite       bool
	}{
		{"/entity/customerorder", "request", "$.positions[].price", FateNativeField, false, true},
		{"/entity/customerorder/{id}", "response", "$.sum", FateReadOnlyDerived, true, false},
		{"/entity/customerorder/{id}", "request", "$.attributes[].value", FateCustomAttribute, false, true},
		{"/v3/posting/fbs/get", "response", "$.result.products[].sku", FateLinkedEntity, true, false},
		{"/v3/posting/fbs/get", "response", "$.result.scanit", FateUnsupported, false, false},
	}
	for _, tc := range tests {
		found := false
		for _, row := range rows {
			if row.Endpoint != tc.endpoint || row.Direction != tc.direction || row.Path != tc.path {
				continue
			}
			found = true
			if row.Fate != tc.fate || row.Proof.LiveRead != tc.liveRead || row.Proof.LiveWrite != tc.liveWrite {
				t.Fatalf("wrong per-field evidence: %+v", row)
			}
		}
		if !found {
			t.Fatalf("missing field coverage: %s %s", tc.endpoint, tc.path)
		}
	}
}

func TestCoverageIsConservativeForNewAndOptionalFields(t *testing.T) {
	t.Parallel()
	m := Manifest{Contracts: []Contract{{Endpoint: "/unknown", Direction: "response", Fields: []Field{
		{Path: "$.unknown", Types: []string{"string"}, Presence: "optional", Nullable: true, Fixture: "synthetic.json"},
	}}}}
	row := m.FieldCoverage()[0]
	if row.Fate != FateUnsupported || row.Proof.Implemented || row.Proof.ContractVerified || !row.Optional || !row.Nullable {
		t.Fatalf("unknown field lost conservative handling: %+v", row)
	}
	row.Types[0] = "modified"
	if m.Contracts[0].Fields[0].Types[0] != "string" {
		t.Fatal("coverage mutated the source manifest")
	}
}

func TestCoverageBlockedNativeUpdatesAreNotAdvertisedAsImplemented(t *testing.T) {
	t.Parallel()
	for _, row := range testManifest(t).FieldCoverage() {
		if row.Direction != "request" || row.Endpoint != "/entity/customerorder/{id}" {
			continue
		}
		if strings.HasPrefix(row.Path, "$.positions") && row.Proof.Implemented {
			if row.Proof.LiveWrite || !strings.Contains(row.Owner, "order_composition.go") || !strings.Contains(strings.Join(row.Prerequisites, ";"), "explicit_collection_replacement_consent") {
				t.Fatalf("collection update exceeded scoped composition coverage: %+v", row)
			}
			continue
		}
		blocked := strings.HasPrefix(row.Path, "$.positions") || strings.HasPrefix(row.Path, "$.organization") || strings.HasPrefix(row.Path, "$.agent")
		if blocked && (row.Fate != FateUnsupported || row.Proof.Implemented || row.Proof.LiveWrite) {
			t.Fatalf("blocked native update gained coverage: %s", row.Path)
		}
	}
}

func TestReconciledAttributeLiveEvidenceIsLimitedToWrittenLeaves(t *testing.T) {
	t.Parallel()
	writes, reads := 0, 0
	for _, row := range testManifest(t).FieldCoverage() {
		if row.Endpoint != "/entity/customerorder/{id}" || !strings.HasPrefix(row.Path, "$.attributes") {
			continue
		}
		written := row.Path == "$.attributes[].meta.href" || row.Path == "$.attributes[].meta.type" || row.Path == "$.attributes[].meta.mediaType" || row.Path == "$.attributes[].value"
		if row.Direction == "request" && row.Proof.LiveWrite {
			if !written || !strings.Contains(row.LiveScope, "RECONCILED") {
				t.Fatal("attribute live write proof exceeded reconciled leaf scope")
			}
			writes++
		}
		if row.Direction == "response" && row.Proof.LiveRead {
			if !written {
				t.Fatal("attribute readback proof expanded to unverified fields")
			}
			reads++
		}
	}
	if writes != 4 || reads != 3 {
		t.Fatalf("unexpected attribute proof counts: writes=%d reads=%d", writes, reads)
	}
}

// These fields were separately asserted by the normal PostgreSQL/nine-process
// merchant browser journey. A validator fixture alone cannot enter this list.
func reviewedRelatedIntegration(row FieldCoverageRow) bool {
	if row.Endpoint != "/v3/posting/fbs/get" || row.Direction != "response" {
		return false
	}
	switch row.Path {
	case "$.result.related_postings", "$.result.related_postings.related_posting_numbers", "$.result.related_postings.related_posting_numbers[]":
		return true
	default:
		return false
	}
}
