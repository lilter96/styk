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
type accountClaimStore interface {
	ClaimForWorker(context.Context, time.Duration, string) (*domain.Task, error)
}
type credentialClaimStore interface {
	ClaimForCredentialWorker(context.Context, time.Duration, string, string) (*domain.Task, error)
}
type PublicationRunner interface {
	RunNext(context.Context) (bool, error)
}
type catalogClaimStore interface {
	ClaimForCatalogWorker(context.Context, time.Duration, string, string) (*domain.Task, error)
}
type Handler interface {
	Process(context.Context, domain.Task) (string, error)
}

// Import handlers own their fenced task completion together with batch persistence.
type ImportHandler interface {
	ProcessImport(context.Context, domain.Task, time.Duration) error
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
	Store                 TaskStore
	Handler               Handler
	Imports               ImportHandler
	Publications          PublicationRunner
	Automatic             PublicationRunner
	Catalog               PublicationRunner
	Orders                PublicationRunner
	StoredCredentials     bool
	ImportAccount         string
	MoyskladImportAccount string
	Log                   *slog.Logger
	Poll, Lease, Retry    time.Duration
}

func (w Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		publicationWorked, automaticWorked := false, false
		if w.Publications != nil {
			worked, err := w.Publications.RunNext(ctx)
			if err != nil && ctx.Err() == nil {
				w.Log.Error("stock publication did not complete")
			}
			publicationWorked = worked && err == nil
		}
		if w.Automatic != nil {
			worked, err := w.Automatic.RunNext(ctx)
			if err != nil && ctx.Err() == nil {
				w.Log.Error("automatic stock cycle did not advance")
			}
			automaticWorked = worked && err == nil
		}
		// Catalog operations use the same worker lifecycle and durable outbox.
		// Give imports a turn even when the catalog queue has ready work.
		catalogWorked := w.advanceCatalog(ctx)
		orderWorked := w.advanceOrders(ctx)
		if err := w.Store.ExpireExhausted(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.Log.Error("expire tasks", "error", err)
		} else {
			var t *domain.Task
			var err error
			if scoped, ok := w.Store.(credentialClaimStore); ok && w.StoredCredentials {
				t, err = scoped.ClaimForCredentialWorker(ctx, w.Lease, w.ImportAccount, w.MoyskladImportAccount)
			} else if scoped, ok := w.Store.(catalogClaimStore); ok {
				t, err = scoped.ClaimForCatalogWorker(ctx, w.Lease, w.ImportAccount, w.MoyskladImportAccount)
			} else if scoped, ok := w.Store.(accountClaimStore); ok {
				t, err = scoped.ClaimForWorker(ctx, w.Lease, w.ImportAccount)
			} else {
				t, err = w.Store.Claim(ctx, w.Lease)
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				w.Log.Error("claim task", "error", err)
			} else if t != nil {
				if t.Type == domain.OzonCatalogTask || t.Type == domain.MoyskladCatalogTask {
					if w.Imports == nil {
						w.Log.Error("import handler unavailable", "task_id", t.ID)
					} else if err := w.Imports.ProcessImport(ctx, *t, w.Lease); err != nil && ctx.Err() == nil {
						// Import errors are deliberately sanitized; provider payloads and
						// credentials must never be included in worker logs.
						w.Log.Error("import attempt did not complete", "task_id", t.ID)
					}
					continue
				}
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
		if catalogWorked || orderWorked || publicationWorked || automaticWorked {
			continue
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

func (w Worker) advanceCatalog(ctx context.Context) bool {
	if w.Catalog == nil {
		return false
	}
	worked, err := w.Catalog.RunNext(ctx)
	if err != nil && ctx.Err() == nil {
		w.Log.Error("catalog synchronization did not advance")
	}
	return worked && err == nil
}

func (w Worker) advanceOrders(ctx context.Context) bool {
	if w.Orders == nil {
		return false
	}
	worked, err := w.Orders.RunNext(ctx)
	if err != nil && ctx.Err() == nil {
		w.Log.Error("order processing requires attention")
	}
	return worked && err == nil
}
