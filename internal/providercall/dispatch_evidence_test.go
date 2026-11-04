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

func dispatchTestTransport(t *testing.T, store Store, calls *atomic.Int32) Transport {
	t.Helper()
	gate, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return Transport{Gate: gate, Scope: Scope{Tenant: "tenant", Provider: "ozon", Account: "account"}, Timeout: time.Second, Base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("synthetic lost response")
	})}
}

func TestDispatchEvidenceSurvivesClientTimeoutDuringAdmission(t *testing.T) {
	t.Parallel()
	for _, duringStart := range []bool{false, true} {
		t.Run(map[bool]string{false: "admission", true: "start"}[duringStart], func(t *testing.T) {
			t.Parallel()
			store := &testStore{waitAdmission: !duringStart, waitStart: duringStart}
			var calls atomic.Int32
			client := &http.Client{Timeout: 15 * time.Millisecond, Transport: dispatchTestTransport(t, store, &calls)}
			ctx, evidence := WithDispatchEvidence(context.Background())
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api-seller.ozon.ru/v2/products/stocks", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Do(request)
			if err == nil || !evidence.AbortBeforeDispatch() || evidence.Started() || calls.Load() != 0 {
				t.Fatal("HTTP timeout lost pre-dispatch certainty", err)
			}
			if duringStart && store.finishes.Load() != 1 {
				t.Fatal("cancelled reservation was not released")
			}
		})
	}
}

func TestDispatchEvidenceNeverInfersUncoordinatedNetworkOutcome(t *testing.T) {
	t.Parallel()
	ctx, evidence := WithDispatchEvidence(context.Background())
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("uncoordinated ambiguous network result")
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api-seller.ozon.ru/v2/products/stocks", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Do(req); err == nil {
		t.Fatal("expected transport failure")
	}
	if evidence.AbortBeforeDispatch() || evidence.Started() {
		t.Fatal("unobserved transport fabricated certainty")
	}
	if enterDispatchEvidence(ctx) == nil {
		t.Fatal("a late transport entered a closed attempt")
	}
}

func TestDispatchEvidenceAbortAndNetworkStartAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	for range 500 {
		ctx, evidence := WithDispatchEvidence(context.Background())
		if err := enterDispatchEvidence(ctx); err != nil {
			t.Fatal(err)
		}
		var startErr error
		var prevented bool
		var group sync.WaitGroup
		group.Go(func() { startErr = startDispatchEvidence(ctx) })
		group.Go(func() { prevented = evidence.AbortBeforeDispatch() })
		group.Wait()
		if prevented && startErr == nil {
			t.Fatal("abort claimed certainty while network start was permitted")
		}
		if !prevented && !evidence.Started() {
			t.Fatal("race lost both start and prevention evidence")
		}
		if evidence.Started() && evidence.AbortBeforeDispatch() {
			t.Fatal("started request was relabelled as prevented")
		}
	}
}

type lateDispatchStore struct {
	testStore
	entered chan struct{}
	release chan struct{}
}

func (s *lateDispatchStore) TryAcquireProviderCall(ctx context.Context, a Attempt) (Admission, error) {
	close(s.entered)
	select {
	case <-ctx.Done():
		return Admission{}, ctx.Err()
	case <-s.release:
		return Admission{Lease: Lease{Attempt: a}}, nil
	}
}

func TestDispatchEvidenceBlocksLateTransportAfterAdmissionReturns(t *testing.T) {
	t.Parallel()
	store := &lateDispatchStore{entered: make(chan struct{}), release: make(chan struct{})}
	var calls atomic.Int32
	transport := dispatchTestTransport(t, store, &calls)
	base, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx, evidence := WithDispatchEvidence(base)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api-seller.ozon.ru/v2/products/stocks", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := transport.RoundTrip(request); done <- err }()
	select {
	case <-store.entered:
	case <-ctx.Done():
		t.Fatal("transport did not enter admission")
	}
	if !evidence.AbortBeforeDispatch() {
		t.Fatal("waiting transport could not be sealed")
	}
	close(store.release)
	select {
	case err = <-done:
		if !IsNotDispatched(err) || calls.Load() != 0 || evidence.Started() {
			t.Fatal("late transport bypassed prevention", err)
		}
	case <-ctx.Done():
		t.Fatal("late transport did not terminate")
	}
}

func TestDispatchEvidenceCannotHideStartedTransportError(t *testing.T) {
	t.Parallel()
	ctx, evidence := WithDispatchEvidence(context.Background())
	if err := enterDispatchEvidence(ctx); err != nil {
		t.Fatal(err)
	}
	if err := startDispatchEvidence(ctx); err != nil {
		t.Fatal(err)
	}
	if evidence.AbortBeforeDispatch() || !evidence.Started() {
		t.Fatal("network-started attempt became a pre-dispatch failure")
	}
}
