package catalogsync

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

func TestRunnerReconcilesThenPreventsLoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.run(t)
	if f.store.works[0].State != StateAcknowledged || f.target.writes != 1 || f.store.beginCount != 1 {
		t.Fatalf("write must be journaled and only acknowledged: %+v", f.store.works[0])
	}
	f.run(t)
	if f.store.works[0].State != StateReconciled || f.store.current.Targets[0].Baseline == nil {
		t.Fatal("readback did not persist the baseline")
	}
	decision := f.preview(t).Decisions[0]
	if !decision.Plan.Noop || len(decision.Commands) != 0 || f.target.writes != 1 {
		t.Fatalf("loop prevention failed: %+v", decision)
	}
}

func TestRunnerRejectsChangedEvidenceBeforeDispatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*fixture)
	}{
		{name: "source revision", change: func(f *fixture) {
			p := f.source.products["source-1"]
			p.Revision = "changed"
			f.source.products[p.Identity.ID] = p
		}},
		{name: "source facts same revision", change: func(f *fixture) {
			p := f.source.products["source-1"]
			p.Fields[catalog.FieldName] = catalog.Text("changed")
			f.source.products[p.Identity.ID] = p
		}},
		{name: "target revision", change: func(f *fixture) {
			p := f.target.products["target-1"]
			p.Revision = "changed"
			f.target.products[p.Identity.ID] = p
		}},
		{name: "config", change: func(f *fixture) { f.store.current.Config.Revision++ }},
		{name: "mapping", change: func(f *fixture) { f.store.current.Targets[0].Mapping.Version++ }},
		{name: "credentials", change: func(f *fixture) { f.store.current.Credentials.Ozon++ }},
		{name: "disabled", change: func(f *fixture) { f.store.current.Config.Enabled = false }},
		{name: "allowlist removed", change: func(f *fixture) { f.runner.AllowScope = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.approve(t)
			tc.change(f)
			f.run(t)
			if f.store.works[0].State != StateBlocked || f.source.writes+f.target.writes != 0 {
				t.Fatalf("stale evidence dispatched: %+v", f.store.works[0])
			}
		})
	}
}

func TestRunnerFinalGuardPreventsWrite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.target.bindHook = func() { f.store.current.Config.Enabled = false }
	f.run(t)
	if f.target.writes != 0 || f.store.beginCount != 0 || f.store.works[0].State != StateBlocked {
		t.Fatal("final guard did not block dispatch")
	}
}

func TestUnknownOutcomeNeverResubmitsAfterRestart(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.target.writeErr = errTestProvider
	f.run(t)
	if f.store.works[0].State != StateUnknown {
		t.Fatal("lost response was not fenced UNKNOWN")
	}
	f.runner.WorkerID = "restarted-worker"
	f.target.writeErr = nil
	f.run(t)
	f.run(t)
	if f.target.writes != 1 || f.store.works[0].State != StateUnknown {
		t.Fatalf("unknown outcome was retried: writes=%d state=%s", f.target.writes, f.store.works[0].State)
	}
}

func TestUnknownWithObservedEffectCanReconcileWithoutWrite(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.target.writeErr, f.target.applyOnError = errTestProvider, true
	f.run(t)
	f.run(t)
	if f.store.works[0].State != StateReconciled || f.target.writes != 1 {
		t.Fatalf("read-only reconciliation failed: state=%s writes=%d", f.store.works[0].State, f.target.writes)
	}
}

func TestAsyncAcceptedPollsAfterRestartWithoutResubmit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.target.accept = true
	f.approve(t)
	f.run(t)
	if f.store.works[0].State != StateProcessing || f.store.works[0].Receipt.TaskID == "" || f.store.current.Targets[0].Baseline != nil {
		t.Fatal("accepted task was not durably PROCESSING")
	}
	f.runner.WorkerID = "restarted-worker"
	f.target.pollState = catalog.ReceiptAccepted
	f.run(t)
	if f.store.works[0].State != StateProcessing {
		t.Fatal("pending task was incorrectly reconciled")
	}
	f.target.pollState = catalog.ReceiptApplied
	f.run(t)
	if f.store.works[0].State != StateAcknowledged {
		t.Fatal("applied task requires readback")
	}
	f.run(t)
	if f.target.writes != 1 || f.target.polls != 2 || f.store.works[0].State != StateReconciled {
		t.Fatalf("async restart: writes=%d polls=%d state=%s", f.target.writes, f.target.polls, f.store.works[0].State)
	}
}

func TestFailedOutcomePersistenceRecoversAsUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.store.failCompletion = true
	f.now = f.now.Add(time.Minute)
	if _, err := f.runner.RunNext(f.ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected persistence failure: %v", err)
	}
	if f.store.works[0].State != StateDispatching || f.target.writes != 1 {
		t.Fatal("dispatch journal was not retained")
	}
	f.store.failCompletion = false
	f.run(t)
	if f.target.writes != 1 || f.store.works[0].State != StateReconciled {
		t.Fatal("expired dispatch was resent instead of read-only recovery")
	}
}
