package ozon

import (
	"context"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogPollSeparatesAcceptanceResultsAndPartialErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, status string
		pid          int64
		problems     []catalogImportError
		expected     catalog.ReceiptState
	}{
		{"pending", "pending", 0, []catalogImportError{}, catalog.ReceiptAccepted},
		{"imported", "imported", 42, []catalogImportError{}, catalog.ReceiptApplied},
		{"skipped", "skipped", 42, []catalogImportError{}, catalog.ReceiptApplied},
		{"imported with errors", "imported", 42, []catalogImportError{{Code: "error_attribute_values_empty", AttributeID: 4191}}, catalog.ReceiptPartial},
		{"failed", "failed", 0, []catalogImportError{{Code: "description_category_not_found"}}, catalog.ReceiptRejected},
		{"failed existing product", "failed", 42, []catalogImportError{{Code: "unknown-sensitive-provider-text"}}, catalog.ReceiptPartial},
		{"unrecognized", "new_provider_status", 42, []catalogImportError{}, catalog.ReceiptUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != catalogImportInfoPath {
					t.Errorf("poll attempted non-status operation %s", r.URL.Path)
					return
				}
				replyCatalogJSON(t, w, map[string]any{"result": map[string]any{"total": 1, "items": []any{map[string]any{"offer_id": "OFFER", "product_id": tc.pid, "status": tc.status, "errors": tc.problems}}}})
			})
			previous := catalog.Receipt{CommandID: "command", TaskID: "91", State: catalog.ReceiptAccepted}
			receipt, err := client.PollCatalog(context.Background(), catalogFixtureCommand(), previous)
			if err != nil || receipt.State != tc.expected || len(receipt.Products) != 1 || receipt.Products[0].Status != tc.status {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if len(tc.problems) > 0 && len(receipt.Errors) != len(tc.problems) {
				t.Fatal("per-product failures were lost")
			}
		})
	}
}

func TestCatalogPollRejectsMissingDuplicateAndForeignResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response any
	}{
		{"missing", map[string]any{"result": map[string]any{"total": 0, "items": []any{}}}},
		{"duplicate", map[string]any{"result": map[string]any{"total": 2, "items": []any{map[string]any{"offer_id": "OFFER"}, map[string]any{"offer_id": "OFFER"}}}}},
		{"foreign offer", map[string]any{"result": map[string]any{"total": 1, "items": []any{map[string]any{"offer_id": "FOREIGN", "product_id": 42, "status": "imported", "errors": []any{}}}}}},
		{"missing error array", map[string]any{"result": map[string]any{"total": 1, "items": []any{map[string]any{"offer_id": "OFFER", "product_id": 42, "status": "imported"}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) { replyCatalogJSON(t, w, tc.response) })
			receipt, err := client.PollCatalog(context.Background(), catalogFixtureCommand(), catalog.Receipt{CommandID: "command", TaskID: "91"})
			if err == nil || receipt.State != catalog.ReceiptUnknown || len(receipt.Products) != 0 {
				t.Fatalf("ambiguous task became success: %+v %v", receipt, err)
			}
		})
	}
}
