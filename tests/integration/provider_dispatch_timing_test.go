//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

func TestProviderDispatchGrantProofRejectsMissingCorruptOrEarlyRows(t *testing.T) {
	t.Parallel()
	now := time.Now()
	valid := ordersProcessGrantEvidence{interval: 20 * time.Millisecond, attempts: 2, starts: []time.Time{now, now.Add(20 * time.Millisecond)}}
	if err := validateOrdersProcessGrants(valid, 2); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		alter    func(*ordersProcessGrantEvidence)
		arrivals int
	}{
		{"missing row", func(e *ordersProcessGrantEvidence) { e.starts = e.starts[:1] }, 2},
		{"corrupt counter", func(e *ordersProcessGrantEvidence) { e.attempts = 1 }, 2},
		{"early grant", func(e *ordersProcessGrantEvidence) { e.starts = []time.Time{now, now.Add(19 * time.Millisecond)} }, 2},
		{"unpermitted network attempt", func(*ordersProcessGrantEvidence) {}, 3},
		{"wrong interval", func(e *ordersProcessGrantEvidence) { e.interval = 19 * time.Millisecond }, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence := valid
			tc.alter(&evidence)
			if validateOrdersProcessGrants(evidence, tc.arrivals) == nil {
				t.Fatal("invalid shared budget proof accepted")
			}
		})
	}
}

func TestProviderFinalGuardDelayReordersArrivalsWithoutBypassingSharedGrant(t *testing.T) {
	db := catalogSyncDB(t)
	arrived := make(chan timedProviderArrival, 2)
	client, server := delayedGuardClient(t, db, arrived)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	delayed := providercall.WithGuard(ctx, func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	done := make(chan error, 2)
	go func() { done <- timedProviderGet(delayed, client, server.URL+"/first") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := timedProviderGet(ctx, client, server.URL+"/second"); err != nil {
		close(release)
		t.Fatal(err)
	}
	first := <-arrived
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	second := <-arrived
	if first.path != "/second" || second.path != "/first" {
		t.Fatal("delayed grant did not demonstrate arrival reordering")
	}
	t.Logf("actual reordered provider arrival gap=%s", second.at.Sub(first.at))
	assertDelayedGuardGrants(t, db.Pool, ctx)
}

func timedProviderGet(ctx context.Context, client *http.Client, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func assertDelayedGuardGrants(t *testing.T, pool *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	var evidence ordersProcessGrantEvidence
	err := pool.QueryRow(ctx, `SELECT a.interval_ns,a.attempts,array_agg(p.started_at ORDER BY p.started_at)
 FROM provider_call_accounts a JOIN provider_call_permits p ON p.provider=a.provider AND p.account_id=a.account_id
 WHERE a.provider='ozon' AND a.account_id='guard-delay' GROUP BY a.interval_ns,a.attempts`).Scan(&evidence.interval, &evidence.attempts, &evidence.starts)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateOrdersProcessGrants(evidence, 2); err != nil {
		t.Fatal("actual DB grants violated the interval", err)
	}
	t.Logf("provider arrival order reversed while durable dispatch grant gap=%s", evidence.starts[1].Sub(evidence.starts[0]))
}

type timedProviderArrival struct {
	path string
	at   time.Time
}

func delayedGuardClient(t *testing.T, db *postgres.Store, arrived chan<- timedProviderArrival) (*http.Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- timedProviderArrival{path: r.URL.Path, at: time.Now()}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond, MaxInflight: 2})
	if err != nil {
		t.Fatal(err)
	}
	client, err := providercall.Bind(&http.Client{}, gate, providercall.Scope{Tenant: "synthetic", Provider: "ozon", Account: "guard-delay"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}
