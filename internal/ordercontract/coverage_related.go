package ordercontract

import "slices"

const relatedCoverageSource = "ozon_assembly_related_review_20261104"

// Related assembly numbers remain source-account observations. Neither an
// observed empty list nor a populated sibling list authorizes reserve transfer.
func coverRelatedPosting(row *FieldCoverageRow) bool {
	kind := relatedCoverageKind(row)
	if kind == "" {
		return false
	}
	supportedCoverage(row, FateStructuredExtension, "internal/infrastructure/ozon/order_related.go",
		"Preserve typed missing/null/empty assembly-related references and account scope; never reinterpret as children, exhaustive membership or parent retirement.")
	row.Prerequisites = []string{"authenticated_source_account_scope", "current_provider_read_authority_and_credentials", "GET_detail_only_related_postings_option", "preserve_missing_null_empty_and_exact_string_identities", "endpoint_schema_remains_partial_unverified", "no_ancestry_reserve_or_fulfillment_write_authority"}
	row.Reason = "audited_read_only_assembly_related_adapter_and_console_path"
	row.Proof.ContractVerified = kind != "list"
	row.Proof.LiveWrite = false
	if kind == "request" {
		row.Transformation = "Send an optional boolean only in the detail read request; never add the unverified flag to list requests."
		row.IntegrationReason = "HTTP adapter unit TestRelatedPostingReadFlagIsDetailOnly asserts the request flag; process fixture does not independently assert request-body selection."
		row.LiveScope = "NOT_OBSERVED_LIVE_REQUEST_BODY; no captured live wire-body assertion"
		return true
	}
	coverRelatedResponseEvidence(row, kind)
	return true
}

func relatedCoverageKind(row *FieldCoverageRow) string {
	if row.Endpoint != "/v3/posting/fbs/get" || row.Version != "v3" || row.Presence != "optional" || !row.Optional || !slices.Contains(row.Sources, relatedCoverageSource) {
		return ""
	}
	for _, field := range []struct{ direction, path, kind, fieldType string }{
		{"request", "$.with.related_postings", "request", "boolean"},
		{"response", "$.result.related_postings", "container", "object"},
		{"response", "$.result.related_postings.related_posting_numbers", "list", "array"},
		{"response", "$.result.related_postings.related_posting_numbers[]", "element", "string"},
	} {
		if row.Direction == field.direction && row.Path == field.path && row.Nullable == (field.kind == "container") && slices.Equal(row.Types, []string{field.fieldType}) {
			return field.kind
		}
	}
	return ""
}

func coverRelatedResponseEvidence(row *FieldCoverageRow, kind string) {
	row.Proof.DeterministicIntegration = true
	row.IntegrationReason = "web/e2e/orders-stack.spec.ts exercises nonempty related references through real Go API, PostgreSQL, scheduler and worker with deterministic provider HTTP; linked-peer navigation asserts the returned item."
	row.Proof.LiveRead = kind == "container" || kind == "list"
	if row.Proof.LiveRead {
		row.LiveScope = "2026_11_04_verified_test_account_read_only: six Ozon HTTP200 reads; two explicit EMPTY related_posting_numbers lists, no live split or fulfillment write"
	} else {
		row.LiveScope = "NOT_OBSERVED_LIVE: live related groups were EMPTY, so no returned string element was exercised"
	}
	if kind == "list" {
		row.Transformation += " Official schema declares string items but omits the array type; selected runtime array validation is not full formal contract verification."
	}
}
