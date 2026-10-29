package main

import (
	"context"
	"time"
)

type orderSecretExpirer interface {
	DeleteExpiredOrderSecrets(context.Context, time.Time, int) (int, error)
}

// Retention remains active when publication is disabled. PostgreSQL decides
// expiry and uses SKIP LOCKED, so independent schedulers can safely share work.
type ordersRetentionWork struct {
	store orderSecretExpirer
	next  time.Time
	now   func() time.Time
	mode  string
}

func (w *ordersRetentionWork) RunNext(ctx context.Context) (bool, error) {
	if w.mode == "dispatch" {
		return false, nil
	}
	now := w.now()
	if now.Before(w.next) {
		return false, nil
	}
	w.next = now.Add(time.Minute)
	_, err := w.store.DeleteExpiredOrderSecrets(ctx, now, 100)
	return false, err
}
