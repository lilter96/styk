package ozon

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/providercall"
)

type stockAdmissionStore struct{ wait bool }

func (s stockAdmissionStore) TryAcquireProviderCall(ctx context.Context, a providercall.Attempt) (providercall.Admission, error) {
	if err := ctx.Err(); err != nil {
		return providercall.Admission{}, err
	}
	if s.wait {
		return providercall.Admission{Delay: time.Minute}, nil
	}
	return providercall.Admission{Lease: providercall.Lease{Attempt: a}}, nil
}
func (stockAdmissionStore) StartProviderCall(context.Context, providercall.Lease) (time.Duration, error) {
	return 0, nil
}
func (stockAdmissionStore) FinishProviderCall(context.Context, providercall.Lease, providercall.Result) error {
	return nil
}

func TestStockClientTimeoutBeforeSharedAdmissionIsNotUnknown(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := stockTestClient(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	c.http.Timeout = 20 * time.Millisecond
	gate, err := providercall.New(stockAdmissionStore{wait: true}, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Coordinate(gate, providercall.Scope{Tenant: "tenant", Provider: "ozon", Account: "seller"}); err != nil {
		t.Fatal(err)
	}
	_, err = c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 1}})
	if !errors.Is(err, ErrStockWriteNotDispatched) || errors.Is(err, ErrStockWriteUnknown) || calls.Load() != 0 {
		t.Fatal("admission wait fabricated uncertain external write", err)
	}
}

func TestStockLocalRateWaitCancellationIsNotUnknown(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := stockTestClient(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	c.next = time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.UpdateStocks(ctx, []StockUpdate{{"123", "456", 1}})
	if !errors.Is(err, ErrStockWriteNotDispatched) || calls.Load() != 0 {
		t.Fatal("local pre-HTTP wait fabricated external uncertainty", err)
	}
}

func TestStockStartedSharedTransportRemainsUnknown(t *testing.T) {
	t.Parallel()
	for _, typedError := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "typed_prevented_error_after_start"}[typedError], func(t *testing.T) {
			t.Parallel()
			c := stockTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected actual HTTP request") })
			var calls atomic.Int32
			c.http.Timeout = 20 * time.Millisecond
			c.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if typedError {
					return nil, &providercall.NotDispatchedError{Cause: context.Canceled}
				}
				<-request.Context().Done()
				return nil, request.Context().Err()
			})
			gate, err := providercall.New(stockAdmissionStore{}, providercall.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Coordinate(gate, providercall.Scope{Tenant: "tenant", Provider: "ozon", Account: "seller"}); err != nil {
				t.Fatal(err)
			}
			_, err = c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 1}})
			if !errors.Is(err, ErrStockWriteUnknown) || errors.Is(err, ErrStockWriteNotDispatched) || calls.Load() != 1 {
				t.Fatal("a potentially sent mutation was called prevented or replayed", err)
			}
		})
	}
}

func TestStockUncoordinatedTimeoutCannotProveNoDispatch(t *testing.T) {
	t.Parallel()
	c := stockTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected actual HTTP request") })
	c.http.Timeout = 20 * time.Millisecond
	var calls atomic.Int32
	c.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	_, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 1}})
	if !errors.Is(err, ErrStockWriteUnknown) || errors.Is(err, ErrStockWriteNotDispatched) || calls.Load() != 1 {
		t.Fatal("uncoordinated network timeout fabricated a local prevention", err)
	}
}
