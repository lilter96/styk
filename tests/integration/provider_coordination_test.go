//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

func providerCoordinator(t testing.TB, db *postgres.Store) *providercall.Coordinator {
	t.Helper()
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return gate
}

func providerRequest(account string) providercall.Request {
	return providercall.Request{Scope: providercall.Scope{Tenant: "provider-test-tenant", Provider: "ozon", Account: account}, Method: "POST /v3/posting/fbs/list", Operation: "orders_read", Endpoint: "http://127.0.0.1:12345", Timeout: time.Second}
}

func providerSend(t testing.TB, gate *providercall.Coordinator, request providercall.Request, status int, headers http.Header) {
	t.Helper()
	p, err := gate.Begin(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = p.Finish(context.Background(), status, headers); err != nil {
		t.Fatal(err)
	}
}

func TestProviderCoordinationResponseScopes(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	t.Run("403 affects method", func(t *testing.T) {
		r := providerRequest("method-denied")
		providerSend(t, gate, r, 403, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := gate.Begin(ctx, r); !errors.Is(err, providercall.ErrMethodForbidden) {
			t.Fatalf("denied method %v", err)
		}
		if _, err := providerCoordinator(t, db).Begin(ctx, r); !errors.Is(err, providercall.ErrMethodForbidden) {
			t.Fatalf("another coordinator lost the persisted method denial: %v", err)
		}
		r.Method = "POST /v3/product/info/list"
		providerSend(t, gate, r, 200, nil)
	})
	t.Run("429 affects account across tenants and methods", func(t *testing.T) {
		r := providerRequest("rate-limited")
		providerSend(t, gate, r, 429, http.Header{"Retry-After": {"1"}})
		r.Scope.Tenant = "another-tenant"
		r.Method = "POST /v3/product/info/list"
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := gate.Begin(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shared cooldown %v", err)
		}
		r.Scope.Account = "unaffected-account"
		providerSend(t, gate, r, 200, nil)
	})
	t.Run("excessive Retry-After never schedules early retry", func(t *testing.T) {
		r := providerRequest("excessive-retry")
		providerSend(t, gate, r, 429, http.Header{"Retry-After": {"999999999999999999999999999999"}})
		if _, err := gate.Begin(context.Background(), r); !errors.Is(err, providercall.ErrCooldownUnbounded) {
			t.Fatalf("unbounded retry %v", err)
		}
	})
	t.Run("successful response quota reset affects every method", func(t *testing.T) {
		r := providerRequest("quota-exhausted-on-success")
		providerSend(t, gate, r, 200, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Lognex-Reset": {"1000"}})
		r.Method = "POST /v3/product/info/list"
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := gate.Begin(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shared quota reset %v", err)
		}
	})
}

func TestProviderCoordinationCredentialRevisionFences(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("credential-fence")
	ctx := context.Background()
	if err := db.RegisterStockConnection(ctx, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account, "provider isolated test", false); err != nil {
		t.Fatal(err)
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'Synthetic',1,true,$4,clock_timestamp(),'provider-test')`, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account, make([]byte, 29))
	if err != nil {
		t.Fatal(err)
	}
	r.Scope.CredentialRevision = 1
	providerSend(t, gate, r, 401, nil)
	if _, err = gate.Begin(ctx, r); !errors.Is(err, providercall.ErrCredentialsRejected) {
		t.Fatalf("401 revision allowed %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE connection_credentials SET revision=2 WHERE tenant_id=$1 AND provider=$2 AND account_id=$3`, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account); err != nil {
		t.Fatal(err)
	}
	r.Scope.CredentialRevision = 2
	providerSend(t, gate, r, 200, nil)
	p, err := gate.Begin(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE connection_credentials SET revision=3 WHERE tenant_id=$1 AND provider=$2 AND account_id=$3`, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account); err != nil {
		t.Fatal(err)
	}
	if err = p.Start(ctx); !errors.Is(err, providercall.ErrCredentialsRejected) {
		t.Fatalf("rotated revision sent %v", err)
	}
	if err = p.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	r.Scope.CredentialRevision = 3
	r.Scope.Tenant = "another-tenant"
	if _, err = gate.Begin(ctx, r); !errors.Is(err, providercall.ErrCredentialsRejected) {
		t.Fatalf("cross tenant credential allowed %v", err)
	}
}

func TestProviderCoordinationTargetSpacingAndCounters(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("target-spacing")
	r.IsWrite = true
	r.TargetKey = "warehouse-1/product-1"
	providerSend(t, gate, r, 200, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := gate.Begin(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stock target interval %v", err)
	}
	var seconds float64
	if err := db.Pool.QueryRow(context.Background(), `SELECT EXTRACT(epoch FROM t.next_send_at-p.started_at)::double precision FROM provider_call_targets t JOIN provider_call_permits p USING(provider,account_id,target_key) WHERE t.account_id=$1`, r.Scope.Account).Scan(&seconds); err != nil || seconds < 120 {
		t.Fatalf("target interval %v: %v", seconds, err)
	}
	r.TargetKey = "warehouse-2/product-1"
	providerSend(t, gate, r, 200, nil)
	r.TargetKey = ""
	p, err := gate.Begin(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	var attempts, completed int64
	if err = db.Pool.QueryRow(context.Background(), `SELECT attempts,completed FROM provider_call_accounts WHERE provider='ozon' AND account_id=$1`, r.Scope.Account).Scan(&attempts, &completed); err != nil || attempts != 2 || completed != 2 {
		t.Fatalf("actual sends %d completions %d: %v", attempts, completed, err)
	}
}

func TestProviderCoordinationWaitReleasesPoolAndLeaseExpires(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("lease-expiration")
	p, err := gate.Begin(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	waitDone := make(chan error, 1)
	go func() { _, waitErr := gate.Begin(waitCtx, r); waitDone <- waitErr }()
	time.Sleep(20 * time.Millisecond)
	if db.Pool.Stat().AcquiredConns() != 0 {
		t.Fatal("waiting gate retains a connection")
	}
	if _, err = db.Pool.Exec(context.Background(), `UPDATE provider_call_permits SET created_at=clock_timestamp()-interval '2 seconds',lease_until=clock_timestamp()-interval '1 second' WHERE account_id=$1`, r.Scope.Account); err != nil {
		t.Fatal(err)
	}
	if err = p.Start(context.Background()); !errors.Is(err, providercall.ErrLeaseLost) {
		t.Fatalf("expired permit sent %v", err)
	}
	if err = <-waitDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait cancellation %v", err)
	}
	next, err := gate.Begin(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	var attempts int64
	if err = db.Pool.QueryRow(context.Background(), `SELECT attempts FROM provider_call_accounts WHERE account_id=$1`, r.Scope.Account).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("expired reservation was counted or replayed: %d %v", attempts, err)
	}
}

func TestProviderCoordinationClockAfterAccountLock(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("clock-after-lock")
	p, err := gate.Begin(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(context.Background(), `UPDATE provider_call_permits SET lease_until=clock_timestamp()+interval '50 milliseconds' WHERE account_id=$1`, r.Scope.Account); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(context.Background(), `SELECT 1 FROM provider_call_accounts WHERE account_id=$1 FOR UPDATE`, r.Scope.Account); err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() { started <- p.Start(context.Background()) }()
	time.Sleep(75 * time.Millisecond)
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-started; !errors.Is(err, providercall.ErrLeaseLost) {
		t.Fatalf("pre-lock clock admitted expired send %v", err)
	}
	if err = p.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
}
