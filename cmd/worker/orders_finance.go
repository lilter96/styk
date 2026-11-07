package main

import (
	"context"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/application"
)

type orderPhaseRunner interface {
	RunNext(context.Context) (bool, error)
	Tick(context.Context) error
}

func (w *ordersWork) scheduleNext(ctx context.Context) error {
	if w.mode == "dispatch" || time.Now().Before(w.nextCheck) {
		return nil
	}
	w.nextCheck = time.Now().Add(time.Second)
	return w.tick(ctx)
}

// Rotate the first dispatch candidate even when that subsystem remains busy.
// Each durable runner retains its own atomic claim and one-attempt guard.
func (w *ordersWork) dispatchNext(ctx context.Context) (bool, error) {
	runners := w.dispatchRunners()
	if len(runners) == 0 {
		return false, nil
	}
	first := w.dispatchTurn % len(runners)
	w.dispatchTurn = (first + 1) % len(runners)
	for offset := range runners {
		index := (first + offset) % len(runners)
		worked, err := runners[index].RunNext(ctx)
		if worked || err != nil {
			return worked, err
		}
	}
	return false, nil
}

func (w *ordersWork) dispatchRunners() []application.PublicationRunner {
	runners := make([]application.PublicationRunner, 0, 3)
	if w.runner != nil {
		runners = append(runners, w.runner)
	}
	if w.finance != nil {
		runners = append(runners, w.finance)
	}
	if w.carriage != nil {
		runners = append(runners, w.carriage)
	}
	return runners
}

func (w *ordersWork) tick(ctx context.Context) error {
	var failures []error
	if w.runner != nil {
		failures = append(failures, w.runner.Tick(ctx))
	}
	if w.finance != nil {
		failures = append(failures, w.finance.Tick(ctx))
	}
	return errors.Join(failures...)
}
