//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersNativeHTTPKnownRejectionRetriesSamePhysicalEventOnce(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	const reference = "native-retry-private-physical-event"
	original := f.proposal(t, orders.ActionDemandCreate, true, reference, "")
	f.approve(t, original)
	f.native.rejectNextNative.Store(http.StatusForbidden)
	f.run(t, orders.StateRejected)
	failed := f.latestWork(t)
	originalHash := nativeRetryWorkHash(t, f, failed.ID)
	if failed.Receipt.HTTPStatus != http.StatusForbidden || failed.DispatchCount != 1 {
		t.Fatal("provider rejection was not preserved")
	}
	ordinary := nativeRetryHTTPProposal(failed, "", reference)
	assertNativeRetryPreviewBlocked(t, f, ordinary)
	request := nativeRetryHTTPProposal(failed, failed.ID, "")
	assertNativeRetryHTTPScope(t, f, request)
	stale := nativeRetryReadyPreview(t, f, request, reference)
	nativeRetryReviseConfig(t, &f)
	if _, err := f.db.OrderActionSecret(f.ctx, failed.Scope, failed.Command.PostingNumber, failed.Command.Action, failed.Command.NativeMovement.Event.ProtectedInput); err == nil {
		t.Fatal("configuration change unlocked ordinary historical action secrets")
	}
	if status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/approve", nativeRetryApprovalBody(t, f, stale), true); status != http.StatusConflict {
		t.Fatalf("stale retry preview approval HTTP %d", status)
	}
	first := nativeRetryReadyPreview(t, f, request, reference)
	second := nativeRetryReadyPreview(t, f, request, reference)
	nativeRetryConcurrentApprove(t, f, first, second)
	nativeRetryAwaitPermission(t, f)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	completed := f.latestWork(t)
	assertNativeRetryReadback(t, f, completed)
	if completed.ID == failed.ID || completed.Command.NativeMovement.Correlation != failed.Command.NativeMovement.Correlation || completed.Command.NativeMovement.Retry.PreviousCommandFingerprint != failed.CommandFingerprint || nativeRetryWorkHash(t, f, failed.ID) != originalHash || f.native.writes.Load() != 2 {
		t.Fatal("retry changed the original rejection or duplicated a provider document")
	}
	assertNativeRetryPreviewBlocked(t, f, ordinary)
	assertNativeRetryPreviewBlocked(t, f, request)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_events`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_retries`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_intents`, 2)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 1)
}

func nativeRetryAwaitPermission(t *testing.T, f nativeMovementHTTPFixture) {
	t.Helper()
	ordersProcessEventually(t, 5*time.Second, "synthetic native permission cooldown", func() bool {
		var denied bool
		err := f.db.Pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM provider_call_methods WHERE provider='moysklad' AND account_id=$1 AND cooldown_until>clock_timestamp())`, f.config.Scope.MoyskladAccount).Scan(&denied)
		return err == nil && !denied
	})
}

func TestOrdersNativeHTTPUnsentBlockedAttemptCanRetrySamePhysicalEvent(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	const reference = "native-retry-unsent-physical-event"
	original := f.proposal(t, orders.ActionDemandCreate, true, reference, "")
	f.approve(t, original)
	f.provider.change(3, 1234)
	f.run(t, orders.StateBlocked)
	blocked := f.latestWork(t)
	originalHash := nativeRetryWorkHash(t, f, blocked.ID)
	if blocked.DispatchCount != 0 || f.native.writes.Load() != 0 {
		t.Fatal("preflight-blocked native attempt reached the provider")
	}
	var intents int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_write_intents WHERE work_id=$1`, blocked.ID).Scan(&intents); err != nil || intents != 0 {
		t.Fatal("blocked native attempt has an external write intent", err)
	}
	f.provider.change(2, 1234)
	f.preview(t)
	assertNativeRetryPreviewBlocked(t, f, nativeRetryHTTPProposal(blocked, "", reference))
	retry := nativeRetryReadyPreview(t, f, nativeRetryHTTPProposal(blocked, blocked.ID, ""), reference)
	f.approve(t, retry)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertNativeRetryReadback(t, f, f.latestWork(t))
	if f.native.writes.Load() != 1 || nativeRetryWorkHash(t, f, blocked.ID) != originalHash {
		t.Fatal("unsent retry changed blocked history or repeated the provider write")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_retries`, 1)
}

func TestOrdersNativeHTTPUnknownAndConflictAreNotKnownRejectionRetries(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	lost := f.proposal(t, orders.ActionDemandCreate, false, "native-retry-unknown-event", "")
	f.approve(t, lost)
	f.native.loseResponse.Store(true)
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	assertNativeRetryPreviewBlocked(t, f, nativeRetryHTTPProposal(unknown, unknown.ID, ""))
	if f.native.writes.Load() != 1 {
		t.Fatal("UNKNOWN was replayed by a retry preview")
	}
	f.run(t, orders.StateReconciled)
	conflict := f.proposal(t, orders.ActionDemandCreate, false, "native-retry-conflict-event", "")
	f.approve(t, conflict)
	f.native.rejectNextNative.Store(http.StatusConflict)
	f.run(t, orders.StateRejected)
	failed := f.latestWork(t)
	if failed.Receipt.HTTPStatus != http.StatusConflict {
		t.Fatal("provider conflict evidence was not preserved")
	}
	assertNativeRetryPreviewBlocked(t, f, nativeRetryHTTPProposal(failed, failed.ID, ""))
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_retries`, 0)
	if f.native.writes.Load() != 2 {
		t.Fatal("ambiguous conflict caused another provider POST")
	}
}

func nativeRetryHTTPProposal(work orders.Work, previousID, reference string) orders.NativeMovementProposal {
	movement := work.Command.NativeMovement
	proposal := orders.NativeMovementProposal{PostingNumber: work.Command.PostingNumber, Action: work.Command.Action, Applicable: movement.Applicable, RetryWorkID: previousID, DemandWorkID: movement.DemandWorkID, EventReference: reference, EventConfirmed: *movement.Applicable.Value, PlannedMoment: time.Now().UTC().Add(time.Hour)}
	for _, line := range movement.Lines {
		proposal.Lines = append(proposal.Lines, orders.ActionQuantity{Identity: line.Identity, Quantity: line.Quantity})
	}
	if previousID == "" {
		proposal.EventKind, proposal.EventOccurredAt = movement.Event.Kind, movement.Event.OccurredAt
	}
	return proposal
}

func nativeRetryHTTPPreview(t *testing.T, f nativeMovementHTTPFixture, proposal orders.NativeMovementProposal) (orders.Preview, []byte) {
	t.Helper()
	body := nativeRetryPreviewBody(t, f.config.Scope, f.config.Revision, proposal)
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/preview", body, true)
	var preview orders.Preview
	if status != http.StatusOK || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("native retry preview HTTP %d", status)
	}
	return preview, raw
}

func nativeRetryPreviewBody(t *testing.T, scope orders.Scope, revision int64, proposal orders.NativeMovementProposal) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": scope.Tenant, "moysklad_account": scope.MoyskladAccount, "ozon_account": scope.OzonAccount, "expected_revision": revision, "posting_numbers": []string{proposal.PostingNumber}, "native_proposals": []orders.NativeMovementProposal{proposal}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func nativeRetryReadyPreview(t *testing.T, f nativeMovementHTTPFixture, proposal orders.NativeMovementProposal, privateReference string) orders.Preview {
	t.Helper()
	preview, raw := nativeRetryHTTPPreview(t, f, proposal)
	decision := preview.Decisions[0]
	if !decision.Ready || len(decision.Commands) != 1 {
		t.Fatalf("explicit native retry blocked: %+v", decision.Validation)
	}
	movement := decision.Commands[0].NativeMovement
	if movement == nil || movement.Retry == nil || movement.Retry.PreviousWorkID != proposal.RetryWorkID || movement.Event.ProtectedInput.Revision != f.config.Revision || bytes.Contains(raw, []byte(privateReference)) {
		t.Fatal("native retry did not restore and protect the original event")
	}
	return preview
}

func assertNativeRetryPreviewBlocked(t *testing.T, f nativeMovementHTTPFixture, proposal orders.NativeMovementProposal) {
	t.Helper()
	preview, _ := nativeRetryHTTPPreview(t, f, proposal)
	if preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) != 0 {
		t.Fatal("ordinary or unsafe native event retry became publishable")
	}
}

func assertNativeRetryHTTPScope(t *testing.T, f nativeMovementHTTPFixture, proposal orders.NativeMovementProposal) {
	t.Helper()
	scope := f.config.Scope
	scope.Tenant = "other-native-retry-merchant"
	status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/preview", nativeRetryPreviewBody(t, scope, f.config.Revision, proposal), true)
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant retry HTTP %d", status)
	}
	wrong := proposal
	wrong.RetryWorkID = "missing-other-target-work"
	assertNativeRetryPreviewBlocked(t, f, wrong)
	wrong = proposal
	wrong.EventReference = "different-physical-event"
	assertNativeRetryPreviewBlocked(t, f, wrong)
	wrong = proposal
	wrong.EventConfirmed = false
	assertNativeRetryPreviewBlocked(t, f, wrong)
}

func nativeRetryReviseConfig(t *testing.T, f *nativeMovementHTTPFixture) {
	t.Helper()
	f.config.IntervalSeconds++
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, IdempotencyKey: "native-retry-current-policy"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
}

func nativeRetryApprovalBody(t *testing.T, f nativeMovementHTTPFixture, preview orders.Preview) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "preview_id": preview.ID, "acknowledge": true, "idempotency_key": "native-retry-" + preview.ID})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func nativeRetryConcurrentApprove(t *testing.T, f nativeMovementHTTPFixture, previews ...orders.Preview) {
	t.Helper()
	type result struct {
		status int
		raw    []byte
	}
	results := make(chan result, len(previews))
	start := make(chan struct{})
	var group sync.WaitGroup
	for _, preview := range previews {
		body := nativeRetryApprovalBody(t, f, preview)
		group.Go(func() {
			<-start
			status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/approve", body, true)
			results <- result{status, raw}
		})
	}
	close(start)
	group.Wait()
	close(results)
	operationID := ""
	for reply := range results {
		if reply.status == http.StatusConflict {
			continue
		}
		var operation orders.Operation
		if reply.status != http.StatusOK || json.Unmarshal(reply.raw, &operation) != nil || operation.ID == "" || operationID != "" && operationID != operation.ID {
			t.Fatalf("concurrent native retry approval HTTP %d", reply.status)
		}
		operationID = operation.ID
	}
	if operationID == "" {
		t.Fatal("all explicit native retry approvals failed")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_retries`, 1)
}

func nativeRetryWorkHash(t *testing.T, f nativeMovementHTTPFixture, id string) string {
	t.Helper()
	var hash string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT md5(work::text) FROM orders_work WHERE id=$1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

func assertNativeRetryReadback(t *testing.T, f nativeMovementHTTPFixture, work orders.Work) {
	t.Helper()
	client, err := f.factory.ResolveOrder(f.ctx, work.Scope, "moysklad", work.Scope.MoyskladAccount, work.Credentials.Moysklad)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := client.Client.(orders.NativeDocumentReader)
	if !ok {
		t.Fatal("native retry independent reader unavailable")
	}
	correlation := work.Command.NativeMovement.Correlation
	correlation.DocumentID = work.Receipt.ExternalID
	actual, err := reader.ReadNativeDocument(f.ctx, orders.NativeDocumentRequest{Scope: work.Scope, Correlation: correlation, IncludeReturns: true})
	if err != nil || !orders.NativeDocumentMatches(work.Command, actual) || !actual.Applicable.IsKnown() || !*actual.Applicable.Value {
		t.Fatal("native retry independent provider readback disagrees", err)
	}
}

func TestOrdersNativeHTTPKnownRejectionExistingDocumentBlocksFurtherEvents(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	const reference = "native-retry-contradictory-existing-document"
	first := f.proposal(t, orders.ActionDemandCreate, false, reference, "")
	f.approve(t, first)
	f.native.applyBeforeRejection.Store(true)
	f.native.rejectNextNative.Store(http.StatusForbidden)
	f.run(t, orders.StateRejected)
	failed := f.latestWork(t)
	originalHash := nativeRetryWorkHash(t, f, failed.ID)
	assertNativeRetryContradictoryDocument(t, f, failed)
	retry := nativeRetryReadyPreview(t, f, nativeRetryHTTPProposal(failed, failed.ID, ""), reference)
	f.approve(t, retry)
	nativeRetryAwaitPermission(t, f)
	f.run(t, orders.StateBlocked)
	blocked := f.latestWork(t)
	if blocked.Code != "NATIVE_RETRY_EXISTING_DOCUMENT_REQUIRES_REVIEW" || blocked.DispatchCount != 0 {
		t.Fatal("unexpected existing document did not preserve its contradiction blocker")
	}
	var noEffect bool
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT orders_native_retry_no_effect($1)`, blocked.ID).Scan(&noEffect); err != nil || noEffect {
		t.Fatal("database classified a known existing document as no application", err)
	}
	next := nativeRetryHTTPProposal(failed, "", "native-retry-different-shipment-event")
	next.Applicable, next.EventKind, next.EventConfirmed = orders.Present(true), "shipment_dispatched", true
	next.EventOccurredAt = time.Now().UTC().Truncate(time.Second)
	assertNativeRetryPreviewBlocked(t, f, next)
	assertNativeRetryPreviewBlocked(t, f, nativeRetryHTTPProposal(blocked, blocked.ID, ""))
	if f.native.writes.Load() != 1 || nativeRetryWorkHash(t, f, failed.ID) != originalHash {
		t.Fatal("contradictory provider evidence caused another POST or changed the original history")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_native_document_results`, 0)
}

func assertNativeRetryContradictoryDocument(t *testing.T, f nativeMovementHTTPFixture, work orders.Work) {
	t.Helper()
	client, err := f.factory.ResolveOrder(f.ctx, work.Scope, "moysklad", work.Scope.MoyskladAccount, work.Credentials.Moysklad)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := client.Client.(orders.NativeDocumentReader)
	if !ok {
		t.Fatal("native contradictory-document reader unavailable")
	}
	actual, err := reader.ReadNativeDocument(f.ctx, orders.NativeDocumentRequest{Scope: work.Scope, Correlation: work.Command.NativeMovement.Correlation, IncludeReturns: true})
	if err != nil || !orders.NativeDocumentMatches(work.Command, actual) || !actual.Applicable.IsKnown() || *actual.Applicable.Value {
		t.Fatal("provider did not independently confirm the contradictory draft document", err)
	}
}
