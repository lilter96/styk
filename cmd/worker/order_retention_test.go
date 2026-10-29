package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retentionRecorder struct {
	calls int
	limit int
	err   error
}

func (r *retentionRecorder) DeleteExpiredOrderSecrets(_ context.Context, _ time.Time, limit int) (int, error) {
	r.calls++
	r.limit = limit
	return 1, r.err
}

func TestOrderRetentionRunsBoundedWithoutPublication(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	recorder := &retentionRecorder{}
	work := &ordersRetentionWork{store: recorder, now: func() time.Time { return now }}
	for range 2 {
		if worked, err := work.RunNext(context.Background()); worked || err != nil {
			t.Fatalf("retention took a publication turn: %v %v", worked, err)
		}
	}
	if recorder.calls != 1 || recorder.limit != 100 {
		t.Fatal("retention is unbounded or runs every worker poll")
	}
	now = now.Add(time.Minute)
	if _, err := work.RunNext(context.Background()); err != nil || recorder.calls != 2 {
		t.Fatalf("next retention tick: %v", err)
	}
	work.mode = "dispatch"
	now = now.Add(time.Minute)
	if _, err := work.RunNext(context.Background()); err != nil || recorder.calls != 2 {
		t.Fatal("dispatch-only process performed scheduler maintenance")
	}
}

func TestOrderRetentionFailureDoesNotSpin(t *testing.T) {
	t.Parallel()
	now := time.Now()
	failure := errors.New("database unavailable")
	recorder := &retentionRecorder{err: failure}
	work := &ordersRetentionWork{store: recorder, now: func() time.Time { return now }}
	if _, err := work.RunNext(context.Background()); !errors.Is(err, failure) {
		t.Fatal("retention failure was hidden")
	}
	if _, err := work.RunNext(context.Background()); err != nil || recorder.calls != 1 {
		t.Fatal("retention failure retried on every worker poll")
	}
}
