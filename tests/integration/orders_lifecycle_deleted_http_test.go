//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersLifecycleHTTPDeletedMovementsRemainUnconfirmedAndBlockReturnBasis(t *testing.T) {
	verifyNativeDeletionContract(t)
	f := newNativeMovementHTTPFixture(t)
	demand := lifecycleDeletedMovement(t, f, orders.ActionDemandCreate, true, "", "lifecycle-deleted-demand")
	setLifecycleDeleted(t, f, demand, true, "2026-11-05 12:00:00.000")
	assertLifecycleReturnBlocked(t, f, demand.ID, "deleted-demand-basis")
	setLifecycleDeleted(t, f, demand, false, nil)
	returned := lifecycleDeletedMovement(t, f, orders.ActionSalesReturnCreate, false, demand.ID, "lifecycle-deleted-draft-return")
	setLifecycleDeleted(t, f, returned, true, "2026-11-05 12:00:00.000")
	assertLifecycleReturnBlocked(t, f, demand.ID, "deleted-related-return")
	if f.native.writes.Load() != 2 || f.native.shipped.Load() != 1 || f.native.returned.Load() != 0 {
		t.Fatal("deletion invented a physical return or duplicated a native write")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 2)
}

func lifecycleDeletedMovement(t *testing.T, f nativeMovementHTTPFixture, action orders.Action, posted bool, demand, event string) orders.Work {
	t.Helper()
	preview := f.proposal(t, action, posted, event, demand)
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	work := f.latestWork(t)
	assertLifecycleDeletedFields(t, f, work)
	setLifecycleDeleted(t, f, work, true, "2026-11-05 12:00:00.000")
	actual := readLifecycleNative(t, f, work)
	if !actual.SourceDeleted.IsKnown() || *actual.SourceDeleted.Value != "2026-11-05 12:00:00.000" {
		t.Fatal("document deletion observation was dropped")
	}
	f.run(t, orders.StatePolling)
	current := f.latestWork(t)
	if current.ID != work.ID || current.DispatchCount != 1 || current.Code != "NATIVE_DOCUMENT_DELETED_REQUIRES_REVIEW" {
		t.Fatal("deleted native document was reconciled or replayed")
	}
	setLifecycleDeleted(t, f, work, true, nil)
	f.run(t, orders.StateReconciled)
	return f.latestWork(t)
}

func assertLifecycleDeletedFields(t *testing.T, f nativeMovementHTTPFixture, work orders.Work) {
	t.Helper()
	for _, tc := range []struct {
		present  bool
		value    any
		expected orders.Presence
	}{
		{false, nil, orders.PresenceMissing}, {true, nil, orders.PresenceNull}, {true, "", orders.PresenceEmpty},
	} {
		setLifecycleDeleted(t, f, work, tc.present, tc.value)
		if actual := readLifecycleNative(t, f, work); actual.SourceDeleted.Presence != tc.expected {
			t.Fatal("missing/null/empty native deletion semantics changed")
		}
	}
}

func readLifecycleNative(t *testing.T, f nativeMovementHTTPFixture, work orders.Work) orders.NativeDocumentObservation {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, work.Scope, "moysklad", work.Scope.MoyskladAccount, work.Credentials.Moysklad)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := binding.Client.(orders.NativeDocumentReader)
	if !ok {
		t.Fatal("native document reader unavailable")
	}
	correlation := work.Command.NativeMovement.Correlation
	correlation.DocumentID = work.Receipt.ExternalID
	actual, err := reader.ReadNativeDocument(f.ctx, orders.NativeDocumentRequest{Scope: work.Scope, Correlation: correlation})
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

func setLifecycleDeleted(t *testing.T, f nativeMovementHTTPFixture, work orders.Work, present bool, value any) {
	t.Helper()
	f.native.mu.Lock()
	defer f.native.mu.Unlock()
	document := f.native.documents[work.Receipt.ExternalID]
	if document == nil {
		t.Fatal("actual native document missing")
	}
	if present {
		document.body["deleted"] = value
	} else {
		delete(document.body, "deleted")
	}
}

func assertLifecycleReturnBlocked(t *testing.T, f nativeMovementHTTPFixture, demand, event string) {
	t.Helper()
	source := f.preview(t).Decisions[0].Posting
	proposal := orders.NativeMovementProposal{PostingNumber: source.PostingNumber, Action: orders.ActionSalesReturnCreate, Applicable: orders.Present(true), Lines: []orders.ActionQuantity{{Identity: source.Lines[0].Identity, Quantity: "1"}}, EventKind: "return_received", EventOccurredAt: time.Now().UTC().Truncate(time.Second), EventConfirmed: true, EventReference: event, DemandWorkID: demand}
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "expected_revision": f.config.Revision, "posting_numbers": []string{source.PostingNumber}, "native_proposals": []orders.NativeMovementProposal{proposal}})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", string(body), true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatal("native return preview unavailable")
	}
	if preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("deleted document authorized a new return")
	}
	found := false
	for _, validation := range preview.Decisions[0].Validation {
		found = found || validation.Code == "native_document_deleted_requires_review"
	}
	if !found {
		t.Fatalf("native deletion diagnostic missing: %+v", preview.Decisions[0].Validation)
	}
}

func verifyNativeDeletionContract(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("../../internal/infrastructure/moysklad/testdata/native_document_deleted_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Sources []struct {
			Entity, URL, Field, Type string
			ReadOnly                 bool `json:"read_only"`
		}
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Sources) != 2 {
		t.Fatal("native deletion contract fixture missing")
	}
	for _, source := range fixture.Sources {
		if !strings.HasPrefix(source.URL, "https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/") || source.Field != "deleted" || source.Type != "DateTime" || !source.ReadOnly {
			t.Fatal("native deletion contract source invalid")
		}
	}
}
