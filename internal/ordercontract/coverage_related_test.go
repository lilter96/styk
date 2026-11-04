package ordercontract

import (
	"reflect"
	"strings"
	"testing"
)

func relatedCoverageRows(t *testing.T) []FieldCoverageRow {
	t.Helper()
	var rows []FieldCoverageRow
	for _, contract := range testManifest(t).Contracts {
		for _, field := range contract.Fields {
			row := coverageBase(contract, field)
			if coverRelatedPosting(&row) {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) != 4 {
		t.Fatalf("exactly four reviewed GET fields required, found %d", len(rows))
	}
	return rows
}

func TestRelatedCoverageSeparatesContractIntegrationAndLiveFieldEvidence(t *testing.T) {
	t.Parallel()
	for _, row := range relatedCoverageRows(t) {
		isRequest := row.Direction == "request"
		isList := row.Path == "$.result.related_postings.related_posting_numbers"
		isContainer := row.Path == "$.result.related_postings"
		if row.Fate != FateStructuredExtension || !row.Proof.Implemented || !row.Proof.ValidatorFixtureCovered || row.Proof.LiveWrite {
			t.Fatalf("incorrect related read implementation fate: %+v", row)
		}
		if row.Proof.ContractVerified == isList || row.Proof.DeterministicIntegration == isRequest || row.Proof.LiveRead != (isList || isContainer) {
			t.Fatalf("related field evidence exceeded the audited assertion: %+v", row)
		}
		if isRequest && !strings.Contains(row.IntegrationReason, "unit") || !isRequest && !strings.Contains(row.IntegrationReason, "orders-stack.spec.ts") {
			t.Fatal("unit and process integration provenance became indistinguishable")
		}
		if !strings.Contains(strings.Join(row.Prerequisites, ";"), "no_ancestry_reserve_or_fulfillment_write_authority") || !strings.Contains(row.Transformation, "never") {
			t.Fatal("assembly references gained lineage/write semantics")
		}
	}
}

func TestRelatedCoverageEmptyLiveListDoesNotClaimLiveStringElements(t *testing.T) {
	t.Parallel()
	liveReads, integrations, verified := 0, 0, 0
	for _, row := range relatedCoverageRows(t) {
		if row.Proof.LiveRead {
			liveReads++
			if !strings.Contains(row.LiveScope, "EMPTY") {
				t.Fatal("live scope lost the actual empty-group limitation")
			}
		}
		if row.Proof.DeterministicIntegration {
			integrations++
		}
		if row.Proof.ContractVerified {
			verified++
		}
		if row.Path == "$.result.related_postings.related_posting_numbers[]" && (!strings.Contains(row.LiveScope, "NOT_OBSERVED_LIVE") || row.Proof.LiveRead) {
			t.Fatal("deterministic nonempty values promoted to real live elements")
		}
	}
	if liveReads != 2 || integrations != 3 || verified != 3 {
		t.Fatalf("wrong distinct evidence counts: live=%d integration=%d formal=%d", liveReads, integrations, verified)
	}
}

func TestRelatedCoverageLeavesUnreviewedFieldsAndContractsUnchanged(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*FieldCoverageRow){
		func(r *FieldCoverageRow) { r.Endpoint = "/v4/posting/fbs/list" },
		func(r *FieldCoverageRow) { r.Version = "v4" },
		func(r *FieldCoverageRow) { r.Direction = "request" },
		func(r *FieldCoverageRow) { r.Path = "$.result.child_posting_numbers" },
		func(r *FieldCoverageRow) { r.Types = []string{"integer"} },
		func(r *FieldCoverageRow) { r.Sources = []string{"unverified_snapshot"} },
		func(r *FieldCoverageRow) { r.Nullable = false },
		func(r *FieldCoverageRow) { r.Presence, r.Optional = "required", false },
	} {
		row := FieldCoverageRow{Endpoint: "/v3/posting/fbs/get", Version: "v3", Direction: "response", Path: "$.result.related_postings", Types: []string{"object"}, Presence: "optional", Optional: true, Nullable: true, Sources: []string{relatedCoverageSource}, Fate: FateUnsupported}
		mutate(&row)
		before := row
		if coverRelatedPosting(&row) || !reflect.DeepEqual(row, before) {
			t.Fatal("unreviewed endpoint, schema or field acquired evidence")
		}
	}
}
