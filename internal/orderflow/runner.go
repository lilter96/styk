package orderflow

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type Collector interface {
	Collect(context.Context, orders.Scope) error
}

type previewReader interface {
	OrderPreview(context.Context, orders.Scope, string) (orders.Preview, error)
}

type RunnerOptions struct {
	WorkerID      string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	AllowScope    func(orders.Scope) bool
	Now           func() time.Time
	Service       *Service
	Collector     Collector
}

// Runner performs one claimed phase per call. Acknowledgements and uncertain
// outcomes can only schedule fresh reads; a write is never automatically retried.
type Runner struct {
	store    orders.RunnerStore
	resolver orders.Resolver
	previews previewReader
	options  RunnerOptions
}

func NewRunner(store orders.RunnerStore, resolver orders.Resolver, options RunnerOptions) (*Runner, error) {
	options = defaultRunnerOptions(options)
	if store == nil || resolver == nil || options.WorkerID == "" || options.LeaseDuration <= 0 || options.LeaseDuration > 5*time.Minute || options.PollInterval <= 0 {
		return nil, orders.ErrInvalid
	}
	reader, ok := store.(previewReader)
	if options.Service != nil {
		reader, ok = options.Service.store, true
	}
	if !ok {
		return nil, orders.ErrInvalid
	}
	return &Runner{store: store, resolver: resolver, previews: reader, options: options}, nil
}

func defaultRunnerOptions(options RunnerOptions) RunnerOptions {
	if options.LeaseDuration == 0 {
		options.LeaseDuration = 3 * time.Minute
	}
	if options.PollInterval == 0 {
		options.PollInterval = 30 * time.Second
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return options
}

func (r *Runner) RunNext(ctx context.Context) (bool, error) {
	now := r.now()
	work, err := r.store.ClaimOrderWork(ctx, orders.ClaimRequest{WorkerID: r.options.WorkerID, Now: now, LeaseUntil: now.Add(r.options.LeaseDuration)})
	if err != nil {
		return false, fmt.Errorf("claim order outbox work: %w", err)
	}
	if work == nil {
		return false, nil
	}
	if err = validateClaimedWork(*work); err != nil {
		return true, r.stop(ctx, *work, "INVALID_DURABLE_INTENT")
	}
	if r.options.AllowScope == nil || !r.options.AllowScope(work.Scope) {
		return true, r.stop(ctx, *work, "TEST_SCOPE_NOT_ALLOWLISTED")
	}
	phase, cancel := context.WithDeadline(ctx, work.LeaseUntil)
	defer cancel()
	if work.State.ReadOnly() {
		return true, r.readback(phase, *work)
	}
	if work.State != orders.StateClaimed {
		return true, fmt.Errorf("claim returned non-runnable order phase: %w", orders.ErrInvalid)
	}
	return true, r.dispatch(phase, *work)
}

func validateClaimedWork(work orders.Work) error {
	if work.ID == "" || work.OperationID == "" || work.PreviewID == "" || work.ClaimToken == "" || work.LeaseUntil.IsZero() || work.Scope != work.Command.Scope {
		return orders.ErrInvalid
	}
	if work.ConfigRevision != work.Command.ConfigRevision || work.Credentials != work.Command.Credentials || work.Command.Validate() != nil {
		return orders.ErrStale
	}
	fingerprint, err := orders.CommandFingerprint(work.Command)
	if err != nil || fingerprint != work.CommandFingerprint {
		return orders.ErrStale
	}
	return nil
}

func (r *Runner) complete(ctx context.Context, work orders.Work, completion orders.Completion) error {
	completion.WorkID, completion.Scope, completion.ClaimToken = work.ID, work.Scope, work.ClaimToken
	completion.ExpectedState, completion.Now = work.State, r.now()
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.store.CompleteOrderWork(save, completion); err != nil {
		return fmt.Errorf("persist order work outcome: %w", err)
	}
	return nil
}

func (r *Runner) stop(ctx context.Context, work orders.Work, code string) error {
	state := orders.StateBlocked
	if work.State.ReadOnly() {
		state = work.State
	}
	return r.complete(ctx, work, orders.Completion{State: state, Code: code, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func (r *Runner) now() time.Time { return r.options.Now().UTC() }

func resolveOrderClient(ctx context.Context, resolver orders.Resolver, scope orders.Scope, provider string, revision int64) (orders.Provider, error) {
	account := scope.OzonAccount
	if provider == "moysklad" {
		account = scope.MoyskladAccount
	}
	binding, err := resolver.ResolveOrder(ctx, scope, provider, account, revision)
	if err != nil {
		return nil, fmt.Errorf("resolve scoped order credentials: %w", err)
	}
	if binding.Scope != scope || binding.Provider != provider || binding.Account != account || binding.CredentialRevision != revision || binding.Client == nil {
		return nil, orders.ErrStale
	}
	return binding.Client, nil
}
