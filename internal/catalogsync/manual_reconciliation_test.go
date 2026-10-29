package catalogsync

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type reconciliationFixtureStore struct {
	claim      ReconciliationClaim
	completion Completion
	calls      int
}

func (s *reconciliationFixtureStore) BeginCatalogReconciliation(context.Context, ReconciliationRequest, time.Time) (ReconciliationClaim, error) {
	s.calls++
	return s.claim, nil
}
func (s *reconciliationFixtureStore) CompleteCatalogReconciliation(_ context.Context, _ ReconciliationClaim, c Completion) (ReconciliationResult, error) {
	s.completion = c
	return ReconciliationResult{ID: s.claim.ID, WorkID: c.WorkID, State: c.State, Code: c.Code, Completed: true}, nil
}

func TestManualReadbackKeepsWarningsAndNeverDispatchesAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.target.accept = true
	f.approve(t)
	f.run(t)
	work := f.store.works[0]
	work.State, work.ClaimToken = StateUnknown, "manual-claim"
	f.target.apply(work.Command)
	issue := catalog.FieldError{Field: catalog.FieldAttributes, Code: "warning_attribute_values_empty", Severity: "warning", AttributeID: 22232, Message: "untrusted provider message"}
	f.target.pollReceipt = &catalog.Receipt{State: catalog.ReceiptPartial, HTTPStatus: 200, ExternalID: work.Command.Target.ID, Errors: []catalog.FieldError{issue}, Products: []catalog.ProductReceipt{{ID: work.Command.Target.ID, OfferID: work.Command.Target.OfferID, Status: "imported", Errors: []catalog.FieldError{issue}}}}
	store := &reconciliationFixtureStore{claim: ReconciliationClaim{ID: "manual-review", Token: work.ClaimToken, Work: work}}
	r := Reconciler{Store: store, Resolver: f.runner.Resolver, AllowScope: f.runner.AllowScope, Now: f.service.Now}
	req := ReconciliationRequest{Scope: work.Scope, WorkID: work.ID, Actor: "operator", IdempotencyKey: "read-only-1", Reason: "Review task warnings", Acknowledge: true, AcceptWarnings: true}
	result, err := r.Reconcile(f.ctx, req)
	if err != nil || result.State != StateReconciled || result.Code != "READBACK_MATCHED_WITH_WARNINGS" {
		t.Fatalf("readback: %+v %v", result, err)
	}
	if f.target.writes != 1 || f.target.polls != 1 || store.completion.Baseline == nil {
		t.Fatal("readback dispatched or omitted selected evidence")
	}
	actual := store.completion.Receipt.Errors[0]
	if actual.Message != "" || actual.Severity != "warning" || actual.AttributeID != 22232 || actual.Code != issue.Code {
		t.Fatalf("warning evidence changed: %+v", actual)
	}
	if store.claim.Work.State != StateUnknown || store.claim.Work.Command.ID != work.Command.ID {
		t.Fatal("approved history was changed")
	}
}

func TestManualReadbackRequiresExplicitAcknowledgment(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := &reconciliationFixtureStore{}
	r := Reconciler{Store: store, Resolver: f.runner.Resolver, AllowScope: f.runner.AllowScope}
	_, err := r.Reconcile(f.ctx, ReconciliationRequest{Scope: f.store.current.Config.Scope, WorkID: "known", Actor: "operator", IdempotencyKey: "readback", Reason: "Check provider result"})
	if err == nil || store.calls != 0 || f.target.polls != 0 || f.target.writes != 0 {
		t.Fatal("unacknowledged action reached provider or durable intent")
	}
}

func TestManualReadbackCompletedIntentIsReadOnlyReplay(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	saved := ReconciliationResult{ID: "saved-review", WorkID: "original", State: StateUnknown, Code: "STILL_UNKNOWN", Completed: true}
	store := &reconciliationFixtureStore{claim: ReconciliationClaim{Result: &saved}}
	r := Reconciler{Store: store, Resolver: f.runner.Resolver, AllowScope: f.runner.AllowScope}
	result, err := r.Reconcile(f.ctx, ReconciliationRequest{Scope: f.store.current.Config.Scope, WorkID: "original", Actor: "operator", IdempotencyKey: "existing", Reason: "Check provider result", Acknowledge: true})
	if err != nil || result != saved || f.target.polls != 0 || f.target.writes != 0 {
		t.Fatal("completed readback repeated external activity")
	}
}
