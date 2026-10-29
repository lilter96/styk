package providercall

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testStore struct {
	acquires        atomic.Int64
	starts          atomic.Int64
	finishes        atomic.Int64
	waitAdmission   bool
	waitStart       bool
	cleanupCanceled atomic.Bool
	failFirstFinish bool
	lastStatus      atomic.Int64
}

func (s *testStore) TryAcquireProviderCall(ctx context.Context, a Attempt) (Admission, error) {
	s.acquires.Add(1)
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}
	if s.waitAdmission {
		return Admission{Delay: time.Hour}, nil
	}
	return Admission{Lease: Lease{Attempt: a}}, nil
}
func (s *testStore) StartProviderCall(ctx context.Context, _ Lease) (time.Duration, error) {
	s.starts.Add(1)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.waitStart {
		return time.Hour, nil
	}
	return 0, nil
}
func (s *testStore) FinishProviderCall(ctx context.Context, _ Lease, result Result) error {
	count := s.finishes.Add(1)
	s.lastStatus.Store(int64(result.Status))
	s.cleanupCanceled.Store(ctx.Err() != nil)
	if s.failFirstFinish && count == 1 {
		return errors.New("coordination unavailable")
	}
	return ctx.Err()
}

func fixtureRequest() Request {
	return Request{Scope: Scope{Tenant: "tenant", Provider: "ozon", Account: "account"}, Method: "POST /v3/posting/fbs/list", Operation: "orders_read"}
}

func TestAdmissionWaitHonorsCancellation(t *testing.T) {
	t.Parallel()
	store := &testStore{waitAdmission: true}
	gate, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err = gate.Begin(ctx, fixtureRequest()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission error %v", err)
	}
	if store.acquires.Load() != 1 || store.starts.Load() != 0 {
		t.Fatal("cancellation admitted an HTTP attempt")
	}
}

func TestPermitCountsOnlyStartAndReleasesAfterCancellation(t *testing.T) {
	t.Parallel()
	store := &testStore{}
	gate, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := gate.Begin(ctx, fixtureRequest())
	if err != nil {
		t.Fatal(err)
	}
	if store.starts.Load() != 0 {
		t.Fatal("admission counted as send")
	}
	if err = p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.Start(ctx); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatal("permit allowed duplicate send")
	}
	cancel()
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if finishErr := p.Finish(ctx, http.StatusOK, nil); finishErr != nil {
				t.Error(finishErr)
			}
		})
	}
	group.Wait()
	if store.starts.Load() != 1 || store.finishes.Load() != 1 || store.cleanupCanceled.Load() {
		t.Fatal("completion lost on cancellation or duplicated")
	}
}

func TestStartWaitCanAbortWithoutSending(t *testing.T) {
	t.Parallel()
	store := &testStore{waitStart: true}
	gate, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := gate.Begin(context.Background(), fixtureRequest())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err = p.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("start %v", err)
	}
	if err = p.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if store.finishes.Load() != 1 || store.cleanupCanceled.Load() {
		t.Fatal("canceled permit not released")
	}
}

func TestAbortPreservesObservedResponseAfterCoordinationFailure(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			store := &testStore{failFirstFinish: true}
			gate, err := New(store, Options{})
			if err != nil {
				t.Fatal(err)
			}
			permit, err := gate.Begin(context.Background(), fixtureRequest())
			if err != nil {
				t.Fatal(err)
			}
			if err = permit.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err = permit.Finish(context.Background(), status, nil); err == nil {
				t.Fatal("completion failure hidden")
			}
			if err = permit.Abort(context.Background()); err != nil {
				t.Fatal(err)
			}
			if store.lastStatus.Load() != int64(status) || store.starts.Load() != 1 {
				t.Fatal("observed response lost or HTTP replayed")
			}
		})
	}
}
