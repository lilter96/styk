package orderflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type ingestionAuthorityStore struct {
	*ingestionTestStore
	renewErr error
}

func (s *ingestionAuthorityStore) RenewOrderIngestion(ctx context.Context, lease orders.IngestionLease, until time.Time) (bool, error) {
	if s.renewErr != nil {
		return false, s.renewErr
	}
	return s.ingestionTestStore.RenewOrderIngestion(ctx, lease, until)
}

type ingestionAuthorityBudget struct {
	started, finished int
	onStart           func()
}

func (*ingestionAuthorityBudget) TryAcquireProviderCall(_ context.Context, attempt providercall.Attempt) (providercall.Admission, error) {
	return providercall.Admission{Lease: providercall.Lease{Attempt: attempt}}, nil
}
func (s *ingestionAuthorityBudget) StartProviderCall(context.Context, providercall.Lease) (time.Duration, error) {
	s.started++
	if s.onStart != nil {
		s.onStart()
	}
	return 0, nil
}
func (s *ingestionAuthorityBudget) FinishProviderCall(ctx context.Context, _ providercall.Lease, _ providercall.Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.finished++
	return nil
}

type ingestionAuthorityHTTP func(*http.Request) (*http.Response, error)

func (f ingestionAuthorityHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ingestionAuthorityFixture(t *testing.T) (*Ingestor, *ingestionAuthorityStore, orders.IngestionLease) {
	t.Helper()
	i, base, _ := newIngestionFixture(t)
	store := &ingestionAuthorityStore{ingestionTestStore: base}
	i.store = store
	now := time.Now()
	lease, err := store.ClaimOrderIngestion(context.Background(), orders.IngestionClaimRequest{Scope: base.config.Scope, Stream: orders.NativePointStream, RefreshKey: "authority-epoch", WorkerID: "authority-test", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	return i, store, *lease
}

func authorityTransport(t *testing.T, scope orders.Scope, budget *ingestionAuthorityBudget, base http.RoundTripper) http.RoundTripper {
	t.Helper()
	gate, err := providercall.New(budget, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return providercall.Transport{Gate: gate, Base: base, Scope: providercall.Scope{Tenant: scope.Tenant, Provider: "moysklad", Account: scope.MoyskladAccount, CredentialRevision: 1}, Timeout: time.Second}
}

func authorityRequest(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://provider.invalid/api/remap/1.2/entity/customerorder", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIngestionReadAuthorityRechecksEveryAttemptAfterSharedAdmission(t *testing.T) {
	t.Parallel()
	i, store, lease := ingestionAuthorityFixture(t)
	ctx, stop := i.ingestionReadAuthority(context.Background(), lease)
	defer stop()
	budget, sent := &ingestionAuthorityBudget{}, 0
	budget.onStart = func() {
		if budget.started == 2 {
			store.config.Revision++
		}
	}
	transport := authorityTransport(t, lease.Scope, budget, ingestionAuthorityHTTP(func(*http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}))
	response, err := transport.RoundTrip(authorityRequest(t, ctx))
	if err != nil || response == nil || sent != 1 {
		t.Fatal("valid first read blocked", err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	response, err = transport.RoundTrip(authorityRequest(t, ctx))
	if response != nil || !providercall.IsNotDispatched(err) || !errors.Is(err, context.Canceled) || sent != 1 || store.renewals != 2 || budget.started != 2 || budget.finished != 2 {
		t.Fatal("configuration revoked after admission still reached the provider", err)
	}
	if !errors.Is(context.Cause(ctx), orders.ErrStale) {
		t.Fatal("safe cancellation lost the stale lease classification")
	}
	assertIngestionAuthorityStopsBatch(t, err)
}

func assertIngestionAuthorityStopsBatch(t *testing.T, err error) {
	t.Helper()
	if code, stop := refreshReadFailure(err); code != "READ_INTERRUPTED" || !stop {
		t.Fatal("final authority denial continued Ozon target reads", code, stop)
	}
	if errors.Is(err, orders.ErrStale) {
		t.Fatal("local lease failure leaked into per-target stale-observation classification")
	}
}

func TestIngestionReadAuthoritySanitizesRevocationAndDatabaseFailures(t *testing.T) {
	t.Parallel()
	for _, denied := range []error{orders.ErrBlocked, orders.ErrStale, errors.New("private-password buyer@example.test")} {
		t.Run("denied", func(t *testing.T) {
			t.Parallel()
			i, store, lease := ingestionAuthorityFixture(t)
			store.renewErr = denied
			ctx, stop := i.ingestionReadAuthority(context.Background(), lease)
			defer stop()
			budget := &ingestionAuthorityBudget{}
			transport := authorityTransport(t, lease.Scope, budget, ingestionAuthorityHTTP(func(*http.Request) (*http.Response, error) {
				t.Fatal("failed read authority reached HTTP")
				return nil, nil
			}))
			response, err := transport.RoundTrip(authorityRequest(t, ctx))
			if response != nil || !providercall.IsNotDispatched(err) || budget.finished != 1 {
				t.Fatal("failed authority was not refused and cleaned up", err)
			}
			if strings.Contains(err.Error(), "private-password") || strings.Contains(context.Cause(ctx).Error(), "buyer@example.test") {
				t.Fatal("raw database failure escaped through cancellation")
			}
			assertIngestionAuthorityStopsBatch(t, err)
		})
	}
}

func TestIngestionReadAuthorityPreservesOrdinaryPerTargetFailureIsolation(t *testing.T) {
	t.Parallel()
	i, _, lease := ingestionAuthorityFixture(t)
	ctx, stop := i.ingestionReadAuthority(context.Background(), lease)
	defer stop()
	transport := authorityTransport(t, lease.Scope, &ingestionAuthorityBudget{}, ingestionAuthorityHTTP(func(*http.Request) (*http.Response, error) {
		return nil, orders.ErrSchemaDrift
	}))
	_, err := transport.RoundTrip(authorityRequest(t, ctx))
	if !errors.Is(err, orders.ErrSchemaDrift) || ctx.Err() != nil {
		t.Fatal("provider field error revoked independent target reads", err)
	}
	if code, stopBatch := refreshReadFailure(err); code != "POSTING_SCHEMA_DRIFT" || stopBatch {
		t.Fatal("per-target Ozon schema failure became account denial")
	}
}
