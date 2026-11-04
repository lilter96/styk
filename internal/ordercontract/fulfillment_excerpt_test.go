package ordercontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

type fulfillmentExcerptRecord struct {
	EvidenceKind string `json:"evidence_kind"`
	Coverage     string `json:"coverage"`
	ExcerptHash  string `json:"excerpt_sha256"`
	Source       struct {
		URL         string  `json:"url"`
		RetrievedAt string  `json:"retrieved_at"`
		RawHash     *string `json:"raw_document_sha256"`
		RawDownload struct {
			Attempts       int    `json:"attempts"`
			MaximumBytes   int    `json:"maximum_bytes"`
			TimeoutSeconds int    `json:"timeout_seconds"`
			Outcome        string `json:"outcome"`
		} `json:"raw_download"`
	} `json:"source"`
	Review  map[string]any `json:"review"`
	Excerpt map[string]any `json:"excerpt"`
}

func reviewedFulfillmentExcerpt(t *testing.T) fulfillmentExcerptRecord {
	t.Helper()
	raw, err := os.ReadFile("testdata/reviewed/ozon_ship_get_2026_11_04.json")
	if err != nil {
		t.Fatal(err)
	}
	var record fulfillmentExcerptRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestFulfillmentExcerptHashesReviewedSubsetWithoutInventingRawSourceEvidence(t *testing.T) {
	t.Parallel()
	record := reviewedFulfillmentExcerpt(t)
	canonical, err := json.Marshal(record.Excerpt)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != record.ExcerptHash {
		t.Fatal("reviewed structural excerpt changed without updating its hash")
	}
	if record.Source.URL != "https://docs.ozon.ru/api/seller/zh/swagger.json" || record.Source.RawDownload.Attempts != 1 || record.Source.RawDownload.MaximumBytes != 20<<20 || record.Source.RawDownload.TimeoutSeconds != 60 || record.Source.RawDownload.Outcome != "PROXY_CONNECT_ABORTED" {
		t.Fatal("actual bounded source retrieval evidence changed")
	}
	if record.EvidenceKind != "REVIEWED_SELECTED_SCHEMA_EXCERPT" || record.Coverage != "PARTIAL_SELECTED_FIELDS_NOT_FULL_SCHEMA" || record.Source.RawHash != nil {
		t.Fatal("selected web review falsely claims complete raw source verification")
	}
}

func TestFulfillmentExcerptCannotPromoteAcknowledgementOr404ToParentRetirement(t *testing.T) {
	t.Parallel()
	record := reviewedFulfillmentExcerpt(t)
	for _, key := range []string{"ship_result_exhaustive_membership_documented", "get_404_proves_split_supersession", "get_specific_404_reason_codes_documented", "rpcStatus_definition_reviewed"} {
		if record.Review[key] != false {
			t.Fatal("selected evidence promoted an undocumented retirement guarantee", key)
		}
	}
	if record.Review["ship_http_200_requires_readback"] != true || record.Review["ship_failed_is_not_success"] != true || record.Review["product_id_semantics"] != "SKU" {
		t.Fatal("actual assembly and identifier prerequisites lost")
	}
	paths := record.Excerpt["paths"].(map[string]any)
	get := paths["/v3/posting/fbs/get"].(map[string]any)["post"].(map[string]any)
	responses := get["response_schemas"].(map[string]any)
	if responses["404"].(map[string]any)["$ref"] != "#/components/schemas/rpcStatus" {
		t.Fatal("generic posting404 changed into a fabricated specific reason")
	}
}

func TestFulfillmentExcerptPreservesOfficialSchemaOmissionsAndDescribedStatus(t *testing.T) {
	t.Parallel()
	record := reviewedFulfillmentExcerpt(t)
	components := record.Excerpt["components"].(map[string]any)["schemas"].(map[string]any)
	response := components["fbsv4FbsPostingShipV4Response"].(map[string]any)
	result := response["properties"].(map[string]any)["result"].(map[string]any)
	if _, invented := result["type"]; invented || result["items"].(map[string]any)["type"] != "string" {
		t.Fatal("selected result schema silently repaired provider omissions")
	}
	detail := components["v3FbsPostingDetail_selected_fields"].(map[string]any)
	status := detail["properties"].(map[string]any)["status"].(map[string]any)
	if _, invented := status["enum"]; invented || record.Review["status_values_are_description_not_formal_enum"] != true || record.Review["described_split_parent_retirement_status"] != "cancelled_from_split_pending" {
		t.Fatal("documented status description became a fabricated closed enum")
	}
	fragments := record.Excerpt["reviewed_description_fragments"].(map[string]any)
	if fragments["v3FbsPostingDetail.properties.status"] != "`cancelled_from_split_pending`——因货件拆分而取消" || record.Review["split_cancel_status_is_not_generic_cancellation_or_financial_settlement"] != true {
		t.Fatal("exact documented split-cancellation fact lost or overinterpreted")
	}
	if _, err := time.Parse(time.RFC3339, record.Source.RetrievedAt); err != nil {
		t.Fatal(err)
	}
}
