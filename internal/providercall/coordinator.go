package providercall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type Coordinator struct {
	store   Store
	options Options
}
type Permit struct {
	store             Store
	lease             Lease
	forbiddenCooldown time.Duration
	mu                sync.Mutex
	started           bool
	finished          bool
}

func New(store Store, options Options) (*Coordinator, error) {
	if store == nil || options.SyntheticInterval < 0 || options.SyntheticInterval > time.Second || options.MaxInflight < 0 || options.MaxInflight > 20 || options.MethodForbiddenCooldown < 0 {
		return nil, ErrInvalid
	}
	if options.MaxInflight == 0 {
		options.MaxInflight = 1
	}
	if options.MethodForbiddenCooldown == 0 {
		options.MethodForbiddenCooldown = time.Minute
	}
	return &Coordinator{store: store, options: options}, nil
}

func (c *Coordinator) Begin(ctx context.Context, request Request) (*Permit, error) {
	a, err := normalize(request, c.options)
	if err != nil {
		return nil, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("provider permit token: %w", err)
	}
	a.ID = hex.EncodeToString(token[:])
	for {
		admission, acquireErr := c.store.TryAcquireProviderCall(ctx, a)
		if acquireErr != nil {
			return nil, fmt.Errorf("admit provider call: %w", acquireErr)
		}
		if admission.Delay <= 0 {
			return &Permit{store: c.store, lease: admission.Lease, forbiddenCooldown: c.options.MethodForbiddenCooldown}, nil
		}
		if err = wait(ctx, admission.Delay); err != nil {
			return nil, err
		}
	}
}

func (p *Permit) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return ErrLeaseLost
	}
	if p.started {
		return ErrAlreadyStarted
	}
	for {
		delay, err := p.store.StartProviderCall(ctx, p.lease)
		if err != nil {
			return fmt.Errorf("start provider call: %w", err)
		}
		if delay <= 0 {
			p.started = true
			return nil
		}
		if err = wait(ctx, delay); err != nil {
			return err
		}
	}
}

func (p *Permit) finish(ctx context.Context, result Result) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return nil
	}
	// Cancellation must still release the reservation and persist a response.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := p.store.FinishProviderCall(cleanup, p.lease, result); err != nil {
		return fmt.Errorf("finish provider call: %w", err)
	}
	p.finished = true
	return nil
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
