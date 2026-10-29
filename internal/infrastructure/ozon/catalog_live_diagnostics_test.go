package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

// The observed task reported imported with two warning diagnostics. Neither
// that status nor warning severity independently verifies the approved field.
func TestCatalogObservedImportWarningsRemainPartialAndRetainDiagnostics(t *testing.T) {
	t.Parallel()
	problems := []map[string]any{
		{"code": "warning_attribute_values_empty", "level": "warning", "state": "new", "attribute_id": 22232},
		{"code": "warning_attribute_values_empty", "level": "warning", "state": "new", "attribute_id": 23536},
	}
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertCatalogTaskRead(t, r)
		replyCatalogJSON(t, w, diagnosticImportResponse(problems))
	})
	receipt, err := client.PollCatalog(t.Context(), catalogFixtureCommand(), diagnosticPreviousReceipt())
	if err != nil || receipt.State != catalog.ReceiptPartial || receipt.HTTPStatus != http.StatusOK {
		t.Fatalf("observed task outcome lost: receipt=%+v err=%v", receipt, err)
	}
	want := []catalog.FieldError{
		{Field: catalog.FieldAttributes, Code: "warning_attribute_values_empty", Severity: "warning", AttributeID: 22232, Message: "Ozon reported an import diagnostic"},
		{Field: catalog.FieldAttributes, Code: "warning_attribute_values_empty", Severity: "warning", AttributeID: 23536, Message: "Ozon reported an import diagnostic"},
	}
	assertCatalogImportDiagnostics(t, receipt, want)
}

func TestCatalogImportDiagnosticSeverityIsExplicitAndSanitized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, level, severity string
		attributeID           int64
		field                 catalog.Field
	}{
		{"warning", "warning", "warning", 22232, catalog.FieldAttributes},
		{"error", "error", "error", 4191, catalog.FieldDescription},
		{"missing severity", "", "unknown", 4180, catalog.FieldName},
		{"untrusted severity", "provider text and credentials", "unknown", -1, catalog.FieldAttributes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problem := map[string]any{"code": "error_attribute_values_empty", "field": "untrusted provider field", "level": tc.level, "attribute_id": tc.attributeID, "message": "untrusted provider message", "description": "untrusted provider description"}
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				assertCatalogTaskRead(t, r)
				replyCatalogJSON(t, w, diagnosticImportResponse([]map[string]any{problem}))
			})
			receipt, err := client.PollCatalog(t.Context(), catalogFixtureCommand(), diagnosticPreviousReceipt())
			if err != nil || receipt.State != catalog.ReceiptPartial || len(receipt.Errors) != 1 {
				t.Fatalf("unverified import became success: receipt=%+v err=%v", receipt, err)
			}
			assertDiagnosticSeverity(t, receipt.Errors[0], tc.severity, tc.field, tc.attributeID)
			assertNoProviderDiagnosticText(t, receipt)
		})
	}
}

func TestCatalogTaskPollUsesActualStatusAndForwardsObserverOnce(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assertCatalogTaskRead(t, r)
		replyCatalogJSON(t, w, diagnosticImportResponse([]map[string]any{}))
	})
	events := make([]catalog.RequestEvent, 0, 1)
	ctx := catalog.WithRequestObserver(t.Context(), func(event catalog.RequestEvent) { events = append(events, event) })
	previous := diagnosticPreviousReceipt()
	previous.HTTPStatus = http.StatusAccepted
	receipt, err := client.PollCatalog(ctx, catalogFixtureCommand(), previous)
	if err != nil || receipt.HTTPStatus != http.StatusOK || receipt.State != catalog.ReceiptApplied || requests.Load() != 1 {
		t.Fatalf("poll reused acceptance status or sent another request: receipt=%+v requests=%d err=%v", receipt, requests.Load(), err)
	}
	want := []catalog.RequestEvent{{Provider: catalog.ProviderOzon, Method: http.MethodPost, Operation: catalogImportInfoPath, Status: http.StatusOK}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("task request observer lost or duplicated: got=%+v want=%+v", events, want)
	}
}

func TestCatalogTaskPollRetainsActualHTTPStatusOnFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   any
	}{
		{"forbidden", http.StatusForbidden, map[string]any{}},
		{"unavailable", http.StatusServiceUnavailable, map[string]any{}},
		{"incomplete contract", http.StatusOK, map[string]any{"result": map[string]any{}}},
		{"out of range attribute", http.StatusOK, diagnosticImportResponse([]map[string]any{{"code": "warning_attribute_values_empty", "attribute_id": json.Number("9223372036854775808")}})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fixtureCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				assertCatalogTaskRead(t, r)
				w.WriteHeader(tc.status)
				replyCatalogJSON(t, w, tc.body)
			})
			client.read.attempts = 1
			receipt, err := client.PollCatalog(t.Context(), catalogFixtureCommand(), diagnosticPreviousReceipt())
			if err == nil || receipt.State != catalog.ReceiptUnknown || receipt.HTTPStatus != tc.status || len(receipt.Products) != 0 {
				t.Fatalf("failed task poll lost actual status: receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestCatalogTaskPollWithoutDispatchHasNoHTTPStatus(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	client := fixtureCatalogClient(t, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	previous := diagnosticPreviousReceipt()
	previous.CommandID = "foreign"
	receipt, err := client.PollCatalog(context.Background(), catalogFixtureCommand(), previous)
	if err == nil || receipt.HTTPStatus != 0 || requests.Load() != 0 {
		t.Fatalf("nonexistent poll was recorded as HTTP: receipt=%+v requests=%d err=%v", receipt, requests.Load(), err)
	}
}

func diagnosticPreviousReceipt() catalog.Receipt {
	return catalog.Receipt{CommandID: "command", TaskID: "91", State: catalog.ReceiptAccepted, HTTPStatus: http.StatusAccepted}
}

func diagnosticImportResponse(problems []map[string]any) map[string]any {
	return map[string]any{"result": map[string]any{"total": 1, "items": []any{
		map[string]any{"offer_id": "OFFER", "product_id": 42, "status": "imported", "errors": problems},
	}}}
}

func assertCatalogTaskRead(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != catalogImportInfoPath {
		t.Errorf("task poll attempted an unexpected operation: %s %s", r.Method, r.URL.Path)
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode task-read body: %v", err)
		return
	}
	if len(body) != 1 || body["task_id"] != "91" {
		t.Errorf("task poll changed the persisted task: %+v", body)
	}
}

func assertCatalogImportDiagnostics(t *testing.T, receipt catalog.Receipt, want []catalog.FieldError) {
	t.Helper()
	if receipt.TaskID != "91" || receipt.ExternalID != "42" || len(receipt.Products) != 1 {
		t.Fatalf("task target identity lost: %+v", receipt)
	}
	product := receipt.Products[0]
	if product.ID != "42" || product.OfferID != "OFFER" || product.Status != "imported" {
		t.Fatalf("provider result target or status lost: %+v", product)
	}
	if !reflect.DeepEqual(receipt.Errors, want) || !reflect.DeepEqual(product.Errors, want) {
		t.Fatalf("provider diagnostics lost: receipt=%+v product=%+v want=%+v", receipt.Errors, product.Errors, want)
	}
}

func assertDiagnosticSeverity(t *testing.T, problem catalog.FieldError, severity string, field catalog.Field, attributeID int64) {
	t.Helper()
	if attributeID < 0 {
		attributeID = 0
	}
	if problem.Severity != severity || problem.Field != field || problem.AttributeID != attributeID || problem.Code != "error_attribute_values_empty" {
		t.Fatalf("diagnostic severity or attribute lost: %+v", problem)
	}
}

func assertNoProviderDiagnosticText(t *testing.T, receipt catalog.Receipt) {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, untrusted := range []string{"provider text and credentials", "untrusted provider"} {
		if strings.Contains(string(data), untrusted) {
			t.Fatal("provider diagnostic text escaped sanitization")
		}
	}
}
