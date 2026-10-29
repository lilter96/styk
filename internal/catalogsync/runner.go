package catalogsync

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type Runner struct {
	Enabled    bool
	WorkerID   string
	Store      RunnerStore
	Resolver   Resolver
	AllowScope func(catalog.Scope) bool
	Now        func() time.Time
}

func (runner Runner) RunNext(ctx context.Context) (bool, error) {
	if !runner.Enabled {
		return false, nil
	}
	if runner.Store == nil || runner.Resolver == nil || !validID(runner.WorkerID) {
		return false, ErrBlocked
	}
	now := runner.now()
	work, err := runner.Store.ClaimCatalogWork(ctx, ClaimRequest{WorkerID: runner.WorkerID, Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil {
		return false, fmt.Errorf("claim catalog outbox work: %w", err)
	}
	if work == nil {
		return false, nil
	}
	if err := work.validate(); err != nil {
		return true, runner.stop(ctx, *work, "INVALID_DURABLE_INTENT")
	}
	if runner.AllowScope == nil || !runner.AllowScope(work.Scope) {
		return true, runner.stop(ctx, *work, "TEST_SCOPE_NOT_ALLOWLISTED")
	}
	switch work.State {
	case StateClaimed:
		return true, runner.dispatch(ctx, *work)
	case StateProcessing:
		return true, runner.poll(ctx, *work)
	case StateAcknowledged, StateUnknown:
		return true, runner.readback(ctx, *work)
	default:
		return true, fmt.Errorf("claim returned non-runnable state: %w", ErrInvalid)
	}
}

func (runner Runner) dispatch(ctx context.Context, work Work) error {
	allowed, err := runner.Store.GuardCatalogDispatch(ctx, DispatchGuard{Work: work, Now: runner.now()})
	if err != nil {
		return fmt.Errorf("check catalog dispatch guard: %w", err)
	}
	if !allowed {
		return runner.stop(ctx, work, "CONFIG_MAPPING_OR_CREDENTIALS_CHANGED")
	}
	clients, err := runner.resolvePair(ctx, work)
	if err != nil {
		return runner.stop(ctx, work, "SCOPED_CREDENTIALS_UNAVAILABLE")
	}
	if !freshDecision(ctx, clients, work) {
		return runner.stop(ctx, work, "SOURCE_OR_TARGET_CHANGED")
	}
	writer := clients.forAccount(work.Command.Target.Account)
	guard := func(check context.Context) (bool, error) {
		return runner.Store.GuardCatalogDispatch(check, DispatchGuard{Work: work, Now: runner.now()})
	}
	if err := writer.BindCatalogIntent(work.Command, guard); err != nil {
		return runner.stop(ctx, work, "INTENT_BINDING_REJECTED")
	}
	allowed, err = runner.Store.GuardCatalogDispatch(ctx, DispatchGuard{Work: work, Now: runner.now(), Begin: true})
	if err != nil {
		return fmt.Errorf("journal catalog dispatch intent: %w", err)
	}
	if !allowed {
		return runner.stop(ctx, work, "FINAL_GUARD_CHANGED")
	}
	work.State = StateDispatching
	return runner.write(ctx, writer, work)
}

func (runner Runner) write(ctx context.Context, writer catalog.Writer, work Work) error {
	send, cancel := context.WithTimeout(ctx, providerTimeout)
	receipt, err := writer.WriteCatalog(send, work.Command)
	cancel()
	if err != nil {
		if validReceiptFor(work.Command, receipt) {
			return runner.recordReceipt(ctx, work, receipt)
		}
		unknown := catalog.Receipt{CommandID: work.Command.ID, State: catalog.ReceiptUnknown, HTTPStatus: receipt.HTTPStatus}
		return runner.complete(ctx, work, Completion{State: StateUnknown, Code: "WRITE_OUTCOME_UNKNOWN", Receipt: unknown, NextRunAt: runner.now().Add(30 * time.Second)})
	}
	return runner.recordReceipt(ctx, work, receipt)
}

func (runner Runner) complete(ctx context.Context, work Work, completion Completion) error {
	completion.WorkID, completion.Scope, completion.ClaimToken = work.ID, work.Scope, work.ClaimToken
	completion.ExpectedState, completion.Now = work.State, runner.now()
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := runner.Store.CompleteCatalogWork(save, completion); err != nil {
		return fmt.Errorf("persist catalog work outcome: %w", err)
	}
	return nil
}

func (runner Runner) stop(ctx context.Context, work Work, code string) error {
	state := StateBlocked
	if work.State == StateUnknown || work.State == StateProcessing || work.State == StateAcknowledged {
		state = work.State
	}
	return runner.complete(ctx, work, Completion{State: state, Code: code, Receipt: work.Receipt,
		CreatedTarget: work.CreatedTarget, NextRunAt: runner.now().Add(30 * time.Second)})
}

func (runner Runner) now() time.Time {
	if runner.Now != nil {
		return runner.Now().UTC()
	}
	return time.Now().UTC()
}

func (work Work) validate() error {
	if !validID(work.ID) || !validID(work.OperationID) || !validID(work.ClaimToken) || !validID(work.EnrollmentID) {
		return ErrInvalid
	}
	if work.Scope != work.Command.Scope || work.ConfigRevision <= 0 || work.Command.PolicyRevision != strconv.FormatInt(work.ConfigRevision, 10) {
		return ErrStale
	}
	if err := catalog.ValidateCommand(work.Command); err != nil {
		return fmt.Errorf("validate frozen catalog command: %w", err)
	}
	fingerprint, err := catalog.CommandFingerprint(work.Command)
	if err != nil || fingerprint != work.CommandFingerprint {
		return ErrStale
	}
	if work.Decision.EnrollmentID != work.EnrollmentID || !work.Decision.actionable() || !work.containsCommand() {
		return ErrStale
	}
	if !work.Scope.Contains(work.Decision.Source.Identity.Account) || !work.Scope.Contains(work.Decision.Target.Identity.Account) {
		return ErrStale
	}
	return nil
}

func (work Work) containsCommand() bool {
	for _, command := range work.Decision.Commands {
		fingerprint, err := catalog.CommandFingerprint(command)
		if err == nil && fingerprint == work.CommandFingerprint {
			return true
		}
	}
	return false
}
