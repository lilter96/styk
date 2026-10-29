package application

import (
	"context"
	"fmt"
	"github.com/lilter96/styk/internal/domain"
	"log/slog"
	"time"
)

type TaskStore interface {
	Claim(context.Context, time.Duration) (*domain.Task, error)
	Finish(context.Context, domain.Task, string, error, time.Duration) error
	ExpireExhausted(context.Context) error
}
type Handler interface {
	Process(context.Context, domain.Task) (string, error)
}
type MockHandler struct{}

func (MockHandler) Process(ctx context.Context, t domain.Task) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.OperationKey == "" || t.ObservationID <= 0 {
		return "", fmt.Errorf("invalid mock task")
	}
	return fmt.Sprintf("mock processed observation %d", t.ObservationID), nil
}

type Worker struct {
	Store              TaskStore
	Handler            Handler
	Log                *slog.Logger
	Poll, Lease, Retry time.Duration
}

func (w Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := w.Store.ExpireExhausted(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.Log.Error("expire tasks", "error", err)
		} else {
			t, err := w.Store.Claim(ctx, w.Lease)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				w.Log.Error("claim task", "error", err)
			} else if t != nil {
				result, processErr := w.Handler.Process(ctx, *t)
				if ctx.Err() != nil {
					return nil
				}
				if err = w.Store.Finish(ctx, *t, result, processErr, w.Retry); err != nil {
					w.Log.Error("finish task", "task_id", t.ID, "error", err)
				} else if processErr != nil {
					w.Log.Error("task failed", "task_id", t.ID, "error", processErr)
				} else {
					w.Log.Info("task completed", "task_id", t.ID)
				}
				continue
			}
		}
		timer := time.NewTimer(w.Poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}
