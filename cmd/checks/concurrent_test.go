package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConcurrentChecksStartTogetherAndRetainEveryFailure(t *testing.T) {
	t.Parallel()
	first, second := errors.New("lint failed"), errors.New("browser failed")
	started, release := make(chan struct{}, 3), make(chan struct{})
	finished := make(chan error, 1)
	checks := make([]namedCheck, 0, 3)
	for i, failure := range []error{first, nil, second} {
		checks = append(checks, namedCheck{name: string(rune('A' + i)), run: func(ctx context.Context) error {
			started <- struct{}{}
			select {
			case <-release:
				return failure
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { finished <- concurrentChecks(ctx, checks) }()
	for range checks {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("independent checks did not start concurrently")
		}
	}
	close(release)
	if err := <-finished; !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("lane failure was lost: %v", err)
	}
}

func TestVerificationBudgetReservesCleanupAndPreservesEarlierCancellation(t *testing.T) {
	t.Parallel()
	started := time.Now()
	ctx, cancel := verificationContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || verificationBudget != 5*time.Minute || verificationCleanupReserve < 20*time.Second || deadline.Sub(started) > 4*time.Minute+41*time.Second {
		t.Fatal("verification work can exceed the wall budget")
	}
	parent, stop := context.WithCancel(context.Background())
	child, finish := verificationContext(parent)
	defer finish()
	stop()
	if !errors.Is(concurrentChecks(child, nil), context.Canceled) {
		t.Fatal("cancelled verification reported success")
	}
}

func TestBusinessTimeoutUsesRemainingSharedDeadlineWithoutRenewal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(10*time.Second))
	defer cancel()
	value, err := businessTimeout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := time.ParseDuration(value)
	if err != nil || remaining <= 0 || remaining > 10*time.Second {
		t.Fatalf("worker renewed the verification budget: %s %v", value, err)
	}
	if _, err = businessTimeout(context.Background()); err == nil {
		t.Fatal("unbounded worker accepted")
	}
	expired, finish := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer finish()
	if _, err = businessTimeout(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired shared deadline accepted: %v", err)
	}
}
