package orderflow

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type carriageRunnerStore interface {
	ClaimCarriageWork(context.Context, orders.ClaimRequest) (*orders.CarriageWork, error)
	GuardCarriageDispatch(context.Context, orders.CarriageWork, bool) (bool, error)
	CompleteCarriageWork(context.Context, orders.CarriageCompletion) error
}
type CarriageRunnerOptions struct {
	WorkerID      string
	AllowScope    func(orders.Scope) bool
	Now           func() time.Time
	LeaseDuration time.Duration
	PollInterval  time.Duration
}
type CarriageRunner struct {
	store    carriageRunnerStore
	resolver orders.Resolver
	options  CarriageRunnerOptions
}

func NewCarriageRunner(store carriageRunnerStore, resolver orders.Resolver, options CarriageRunnerOptions) (*CarriageRunner, error) {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = 3 * time.Minute
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Minute
	}
	if store == nil || resolver == nil || options.WorkerID == "" || options.LeaseDuration <= 0 || options.LeaseDuration > 5*time.Minute || options.PollInterval <= 0 {
		return nil, orders.ErrInvalid
	}
	return &CarriageRunner{store: store, resolver: resolver, options: options}, nil
}

func (r *CarriageRunner) RunNext(ctx context.Context) (bool, error) {
	now := r.now()
	work, err := r.store.ClaimCarriageWork(ctx, orders.ClaimRequest{WorkerID: r.options.WorkerID, Now: now, LeaseUntil: now.Add(r.options.LeaseDuration)})
	if err != nil || work == nil {
		return false, err
	}
	if work.Plan.Validate() != nil || work.Scope != work.Plan.Scope || work.ConfigRevision != work.Plan.ConfigRevision || work.Credentials != work.Plan.Credentials || work.ClaimToken == "" || work.LeaseUntil.IsZero() {
		return true, r.stop(ctx, *work, "CARRIAGE_INVALID_DURABLE_INTENT")
	}
	if r.options.AllowScope == nil || !r.options.AllowScope(work.Scope) {
		return true, r.stop(ctx, *work, "CARRIAGE_SCOPE_NOT_AUTHORIZED")
	}
	phase, cancel := context.WithDeadline(ctx, work.LeaseUntil)
	defer cancel()
	if work.State.ReadOnly() {
		return true, r.readback(phase, *work)
	}
	return true, r.dispatch(phase, *work)
}

func (r *CarriageRunner) clients(ctx context.Context, work orders.CarriageWork) (orders.CarriageReader, orders.CarriageWriter, error) {
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return nil, nil, err
	}
	reader, ok := client.(orders.CarriageReader)
	if !ok {
		return nil, nil, orders.ErrBlocked
	}
	writer, ok := client.(orders.CarriageWriter)
	if !ok {
		return nil, nil, orders.ErrBlocked
	}
	return reader, writer, nil
}

func (r *CarriageRunner) dispatch(ctx context.Context, work orders.CarriageWork) error {
	allowed, err := r.store.GuardCarriageDispatch(ctx, work, false)
	if err != nil {
		return err
	}
	if !allowed {
		return r.stop(ctx, work, "CARRIAGE_AUTHORITY_CHANGED")
	}
	reader, writer, err := r.clients(ctx, work)
	if err != nil {
		return r.stop(ctx, work, "CARRIAGE_ADAPTER_UNAVAILABLE")
	}
	actual, err := reader.ReadCarriageBatch(r.guardedReads(ctx, work), work.Scope, work.Plan.CarriageInput)
	if err != nil || !work.Plan.MatchesMembers(actual) || actual.Fingerprint != work.Plan.Snapshot.Fingerprint {
		return r.stop(ctx, work, "CARRIAGE_BATCH_CHANGED")
	}
	return r.write(ctx, work, writer)
}

func (r *CarriageRunner) write(ctx context.Context, work orders.CarriageWork, writer orders.CarriageWriter) error {
	var began atomic.Bool
	authorize := func(check context.Context) (bool, error) {
		deadline := work.LeaseUntil
		if bound, ok := check.Deadline(); ok && bound.Before(deadline) {
			deadline = bound
		}
		fresh, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		if began.Load() {
			return false, orders.ErrConflict
		}
		allowed, err := r.store.GuardCarriageDispatch(fresh, work, true)
		if allowed && err == nil {
			began.Store(true)
		}
		return allowed, err
	}
	receipt, writeErr := writer.WriteCarriage(r.guardedReads(ctx, work), work.Plan, authorize)
	if !began.Load() {
		return r.stop(ctx, work, "CARRIAGE_FINAL_ADMISSION_BLOCKED")
	}
	work.State, work.DispatchCount = orders.StateDispatching, 1
	if receipt.CommandID != work.Plan.ID || writeErr != nil || receipt.State != orders.StateAcknowledged {
		receipt.State, receipt.Code = orders.StateUnknown, "CARRIAGE_WRITE_OUTCOME_UNKNOWN"
	}
	receipt.CommandID = work.Plan.ID
	return r.complete(ctx, work, orders.CarriageCompletion{State: receipt.State, Code: receipt.Code, Receipt: receipt, CarriageID: receipt.ExternalID, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *CarriageRunner) guardedReads(ctx context.Context, work orders.CarriageWork) context.Context {
	return providercall.WithGuard(ctx, func(check context.Context) error {
		allowed, err := r.store.GuardCarriageDispatch(check, work, false)
		if err != nil {
			return err
		}
		if !allowed {
			return orders.ErrBlocked
		}
		return nil
	})
}

func (r *CarriageRunner) readback(ctx context.Context, work orders.CarriageWork) error {
	allowed, err := r.store.GuardCarriageDispatch(ctx, work, false)
	if err != nil {
		return err
	}
	if !allowed {
		return r.stop(ctx, work, "CARRIAGE_READ_AUTHORITY_CHANGED")
	}
	reader, _, err := r.clients(ctx, work)
	if err != nil {
		return r.stop(ctx, work, "CARRIAGE_READBACK_UNAVAILABLE")
	}
	if work.CarriageID == "" {
		actual, readErr := reader.ReadCarriageBatch(r.guardedReads(ctx, work), work.Scope, work.Plan.CarriageInput)
		if readErr != nil {
			return r.stop(ctx, work, "CARRIAGE_UNKNOWN_CREATE_DISCOVERY_UNAVAILABLE")
		}
		return r.complete(ctx, work, orders.CarriageCompletion{State: orders.StateUnknown, Code: "CARRIAGE_UNKNOWN_CREATE_IDENTITY_REQUIRES_REVIEW", Receipt: work.Receipt, Observation: &actual, NextRunAt: r.now().Add(r.options.PollInterval)})
	}
	actual, err := reader.ReadCarriage(r.guardedReads(ctx, work), work.Scope, work.CarriageID)
	if err != nil {
		return r.stop(ctx, work, "CARRIAGE_READBACK_UNAVAILABLE")
	}
	state, code := work.State, "CARRIAGE_READBACK_MISMATCH"
	if work.OutcomeMatches(actual) {
		state, code = orders.StateReconciled, "CARRIAGE_DOCUMENT_INDEPENDENTLY_CONFIRMED"
	}
	return r.complete(ctx, work, orders.CarriageCompletion{State: state, Code: code, Receipt: work.Receipt, Observation: &actual, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *CarriageRunner) stop(ctx context.Context, work orders.CarriageWork, code string) error {
	state := orders.StateBlocked
	if work.State.ReadOnly() {
		state = work.State
	}
	return r.complete(ctx, work, orders.CarriageCompletion{State: state, Code: code, Receipt: work.Receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *CarriageRunner) complete(ctx context.Context, work orders.CarriageWork, result orders.CarriageCompletion) error {
	result.WorkID, result.Scope, result.ClaimToken = work.ID, work.Scope, work.ClaimToken
	result.ExpectedState, result.Now = work.State, r.now()
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.store.CompleteCarriageWork(save, result); err != nil {
		return fmt.Errorf("persist carriage outcome: %w", err)
	}
	return nil
}

func (r *CarriageRunner) now() time.Time { return r.options.Now().UTC() }
