package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

type catalogWorkerStore struct{ claims int }

func (s *catalogWorkerStore) Claim(context.Context, time.Duration) (*domain.Task, error) {
	s.claims++
	return nil, nil
}
func (*catalogWorkerStore) Finish(context.Context, domain.Task, string, error, time.Duration) error {
	return nil
}
func (*catalogWorkerStore) ExpireExhausted(context.Context) error { return nil }

type catalogWorkerRunner struct {
	calls  int
	cancel context.CancelFunc
}

func (r *catalogWorkerRunner) RunNext(context.Context) (bool, error) {
	r.calls++
	if r.calls == 2 {
		r.cancel()
	}
	return true, nil
}

func TestCatalogWorkGivesImportsATurnAndStopsWithWorker(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &catalogWorkerStore{}
	runner := &catalogWorkerRunner{cancel: cancel}
	worker := Worker{Store: store, Handler: MockHandler{}, Catalog: runner, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Poll: time.Hour}
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 || store.claims < 1 {
		t.Fatalf("catalog runner bypassed worker lifecycle/import fairness: runs=%d claims=%d", runner.calls, store.claims)
	}
}
