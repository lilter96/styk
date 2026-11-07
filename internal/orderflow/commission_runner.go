package orderflow

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type commissionRunnerStore interface {
	ClaimCommissionWork(context.Context, orders.ClaimRequest) (*orders.CommissionWork, error)
	GuardCommissionDispatch(context.Context, orders.CommissionWork, bool) (bool, error)
	CompleteCommissionWork(context.Context, orders.CommissionCompletion) error
}

type commissionReportObserver interface {
	ObserveCommissionReport(context.Context, orders.RealizationReport) error
}

type CommissionRunnerOptions struct {
	Service       *Service
	WorkerID      string
	AllowScope    func(orders.Scope) bool
	Now           func() time.Time
	LeaseDuration time.Duration
	PollInterval  time.Duration
}

type CommissionRunner struct {
	store    commissionRunnerStore
	observer commissionReportObserver
	resolver orders.Resolver
	options  CommissionRunnerOptions
}

func NewCommissionRunner(store commissionRunnerStore, resolver orders.Resolver, options CommissionRunnerOptions) (*CommissionRunner, error) {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = 3 * time.Minute
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Minute
	}
	if store == nil || resolver == nil || options.Service == nil || options.WorkerID == "" || options.LeaseDuration <= 0 || options.LeaseDuration > 5*time.Minute || options.PollInterval <= 0 {
		return nil, orders.ErrInvalid
	}
	observer, ok := store.(commissionReportObserver)
	if !ok {
		return nil, orders.ErrInvalid
	}
	return &CommissionRunner{store: store, observer: observer, resolver: resolver, options: options}, nil
}

func (r *CommissionRunner) RunNext(ctx context.Context) (bool, error) {
	now := r.now()
	work, err := r.store.ClaimCommissionWork(ctx, orders.ClaimRequest{WorkerID: r.options.WorkerID, Now: now, LeaseUntil: now.Add(r.options.LeaseDuration)})
	if err != nil || work == nil {
		return false, err
	}
	if err = validateCommissionWork(*work); err != nil {
		return true, r.stop(ctx, *work, "INVALID_FINANCE_DURABLE_INTENT")
	}
	if r.options.AllowScope == nil || !r.options.AllowScope(work.Scope) {
		return true, r.stop(ctx, *work, "FINANCE_SCOPE_NOT_AUTHORIZED")
	}
	phase, cancel := context.WithDeadline(ctx, work.LeaseUntil)
	defer cancel()
	if work.State.ReadOnly() {
		return true, r.readback(phase, *work)
	}
	if work.State != orders.StateClaimed {
		return true, orders.ErrInvalid
	}
	return true, r.dispatch(phase, *work)
}

func validateCommissionWork(work orders.CommissionWork) error {
	if work.ID == "" || work.PreviewID == "" || work.ClaimToken == "" || work.LeaseUntil.IsZero() || work.Scope != work.Plan.Scope || work.ConfigRevision != work.Plan.ConfigRevision || work.Credentials != work.Plan.Credentials {
		return orders.ErrInvalid
	}
	return work.Plan.Validate()
}

func (r *CommissionRunner) complete(ctx context.Context, work orders.CommissionWork, completion orders.CommissionCompletion) error {
	completion.WorkID, completion.Scope, completion.ClaimToken = work.ID, work.Scope, work.ClaimToken
	completion.ExpectedState, completion.Now = work.State, r.now()
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.store.CompleteCommissionWork(save, completion); err != nil {
		return fmt.Errorf("persist monthly commission result: %w", err)
	}
	return nil
}

func (r *CommissionRunner) stop(ctx context.Context, work orders.CommissionWork, code string) error {
	state := orders.StateBlocked
	if work.State.ReadOnly() {
		state = work.State
	}
	return r.complete(ctx, work, orders.CommissionCompletion{State: state, Code: code, Receipt: work.Receipt, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *CommissionRunner) now() time.Time { return r.options.Now().UTC() }

func (r *CommissionRunner) guardedReads(ctx context.Context, work orders.CommissionWork) context.Context {
	return providercall.WithGuard(ctx, func(check context.Context) error {
		allowed, err := r.store.GuardCommissionDispatch(check, work, false)
		if err != nil {
			return err
		}
		if !allowed {
			return orders.ErrBlocked
		}
		return nil
	})
}
