package orderflow

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func dispatchedFlow(t *testing.T) (*flowStore, *flowProvider, *flowProvider, *Runner) {
	t.Helper()
	review := flowReview(t, "posting")
	store := &flowStore{review: review}
	source := &flowProvider{posting: review.Postings[0]}
	target := &flowProvider{correlateErr: orders.ErrNotFound,
		receipt: orders.Receipt{State: orders.StateAcknowledged, HTTPStatus: 200, ExternalID: "native-order", ObservedAt: flowTestNow}}
	resolver := flowResolver{source: source, target: target}
	service := flowService(t, store, resolver)
	preview := flowPreview(t, service, review)
	work := flowWork(t, preview)
	store.work = &work
	return store, source, target, flowRunner(t, store, resolver, service)
}

func runFlow(t *testing.T, runner *Runner) {
	t.Helper()
	ran, err := runner.RunNext(context.Background())
	if err != nil || !ran {
		t.Fatalf("run next: worked=%v, error=%v", ran, err)
	}
}

func exposeWrittenTarget(store *flowStore, target *flowProvider) {
	target.target = flowTarget(store.work.Command)
	target.correlation, target.correlateErr = target.target.Correlation, nil
}

func TestRunnerAcknowledgementRequiresFutureCompleteReadback(t *testing.T) {
	t.Parallel()
	store, source, target, runner := dispatchedFlow(t)
	target.onWrite = func() {
		if store.work.State != orders.StateDispatching || store.intents != 1 {
			t.Error("HTTP mutation preceded the durable dispatch intent")
		}
	}
	runFlow(t, runner)
	if store.work.State != orders.StateAcknowledged || len(store.completions) != 1 || store.completions[0].ObservedTarget != nil {
		t.Fatalf("HTTP acknowledgement became a reconciliation: %+v", store.completions)
	}
	exposeWrittenTarget(store, target)
	runFlow(t, runner)
	completion := store.completions[1]
	if completion.State != orders.StateReconciled || completion.ExpectedState != orders.StateAcknowledged || completion.ClaimToken != "claim-2" || completion.ObservedTarget == nil || completion.Correlation == nil {
		t.Fatalf("complete readback was not atomically reconciled: %+v", completion)
	}
	if target.writes != 1 || source.reads != 2 {
		t.Fatalf("unexpected write replay or missing fresh source read: writes=%d reads=%d", target.writes, source.reads)
	}
	ran, err := runner.RunNext(context.Background())
	if err != nil || ran {
		t.Fatal("terminal work prevented a fair empty queue result")
	}
}

func TestRunnerAmbiguousOutcomeNeverResends(t *testing.T) {
	t.Parallel()
	store, _, target, runner := dispatchedFlow(t)
	target.writeErr = context.DeadlineExceeded
	runFlow(t, runner)
	if store.work.State != orders.StateUnknown {
		t.Fatalf("ambiguous write did not become unknown: %s", store.work.State)
	}
	runFlow(t, runner)
	if store.work.State != orders.StateUnknown || target.writes != 1 {
		t.Fatalf("missing correlation triggered a resend: state=%s writes=%d", store.work.State, target.writes)
	}
	exposeWrittenTarget(store, target)
	runFlow(t, runner)
	if store.work.State != orders.StateReconciled || target.writes != 1 || store.intents != 1 {
		t.Fatalf("unknown outcome did not reconcile from facts: state=%s writes=%d intents=%d", store.work.State, target.writes, store.intents)
	}
}

func TestRunnerFreshSourceComparesFactsDespiteOpaqueRevision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		change  func(*flowProvider)
		allowed bool
	}{{name: "same opaque revision changed facts", change: func(source *flowProvider) { source.posting.Status = orders.Present("cancelled") }},
		{name: "new opaque revision same facts", allowed: true, change: func(source *flowProvider) { source.posting.Evidence.Revision = "new-opaque-version" }},
		{name: "protected reference differs", allowed: true, change: func(source *flowProvider) {
			source.posting.Customer = orders.ProtectedCustomer{Presence: orders.PresenceValue}
		}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, source, target, runner := dispatchedFlow(t)
			test.change(source)
			runFlow(t, runner)
			if (target.writes == 1) != test.allowed {
				t.Fatalf("fresh source decision ignored facts: state=%s writes=%d", store.work.State, target.writes)
			}
			if !test.allowed && store.work.State != orders.StateBlocked {
				t.Fatalf("changed unsent source was not blocked: %s", store.work.State)
			}
		})
	}
}

func TestRunnerFinalGuardRunsAfterProviderWaitAndFencesDeniedDispatch(t *testing.T) {
	t.Parallel()
	store, _, target, runner := dispatchedFlow(t)
	target.beforeGuard = func() { store.beginDenied = true }
	runFlow(t, runner)
	if store.work.State != orders.StateBlocked || target.writes != 0 || store.intents != 0 {
		t.Fatalf("authority changed during provider wait: state=%s writes=%d intents=%d", store.work.State, target.writes, store.intents)
	}
	if store.completions[0].ExpectedState != orders.StateClaimed {
		t.Fatal("denied pre-dispatch work used an invented dispatch state")
	}
}

func TestRunnerRequiresProviderToEnterDurableGuard(t *testing.T) {
	t.Parallel()
	store, _, target, runner := dispatchedFlow(t)
	target.skipGuard = true
	runFlow(t, runner)
	if store.work.State != orders.StateBlocked || store.intents != 0 || store.completions[0].Receipt.State != "" {
		t.Fatalf("unguarded provider acknowledgement was trusted: %+v", store.completions)
	}
}

func TestRunnerUnknownWithChangedSourceReadsTargetButCannotReconcile(t *testing.T) {
	t.Parallel()
	store, source, target, runner := dispatchedFlow(t)
	target.writeErr = context.DeadlineExceeded
	runFlow(t, runner)
	exposeWrittenTarget(store, target)
	source.posting.Status = orders.Present("cancelled")
	runFlow(t, runner)
	if target.reads != 1 || target.writes != 1 || store.work.State != orders.StateUnknown || store.work.Code != "SOURCE_CHANGED_REQUIRES_REVIEW" {
		t.Fatalf("unknown operation ignored changed source: reads=%d writes=%d state=%s code=%s", target.reads, target.writes, store.work.State, store.work.Code)
	}
}

func TestRunnerPartialTargetReadbackCannotReconcile(t *testing.T) {
	t.Parallel()
	store, _, target, runner := dispatchedFlow(t)
	runFlow(t, runner)
	exposeWrittenTarget(store, target)
	target.target.Complete = false
	runFlow(t, runner)
	if store.work.State == orders.StateReconciled || target.writes != 1 {
		t.Fatal("truncated target positions were accepted or the write was resent")
	}
}

func TestRunnerCompletionPreservesExactClaimCAS(t *testing.T) {
	t.Parallel()
	store, _, target, runner := dispatchedFlow(t)
	target.onWrite = func() { store.staleSave = true }
	ran, err := runner.RunNext(context.Background())
	if !ran || !errors.Is(err, orders.ErrStale) || len(store.completions) != 0 || store.work.State != orders.StateDispatching {
		t.Fatalf("late HTTP completion escaped claim fencing: worked=%v error=%v state=%s", ran, err, store.work.State)
	}
}

func TestRunnerPreexistingCorrelationUsesReadbackWithoutCreation(t *testing.T) {
	t.Parallel()
	store, _, target, runner := dispatchedFlow(t)
	exposeWrittenTarget(store, target)
	runFlow(t, runner)
	if store.work.State != orders.StateReconciled || target.writes != 0 || store.intents != 0 {
		t.Fatalf("existing exact correlation triggered duplicate creation: state=%s writes=%d", store.work.State, target.writes)
	}
}

func TestRunnerUpdateRequiresFreshFullTargetEvenWithStaticRevision(t *testing.T) {
	t.Parallel()
	review, observation := existingFlowReview(t)
	observation.Fields.StateID = "old-state"
	store := &flowStore{review: review}
	source, target := &flowProvider{posting: review.Postings[0]}, &flowProvider{target: observation, correlation: observation.Correlation}
	resolver := flowResolver{source: source, target: target}
	service := flowService(t, store, resolver)
	preview := flowPreview(t, service, review)
	work := flowWork(t, preview)
	store.work = &work
	target.target.Positions = append([]orders.TargetPosition(nil), observation.Positions...)
	target.target.Positions[0].Quantity = "999"
	runFlow(t, flowRunner(t, store, resolver, service))
	if store.work.State != orders.StateBlocked || target.writes != 0 {
		t.Fatalf("static target revision concealed changed facts: state=%s writes=%d", store.work.State, target.writes)
	}
}
