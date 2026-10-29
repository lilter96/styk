//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/providercall"
)

func TestProviderProbeCorrectedCredentialAfter401KeepsGlobalQuota(t *testing.T) {
	db := catalogSyncDB(t)
	gate, err := providercall.New(db, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	scope := providercall.Scope{Tenant: "probe-tenant", Provider: "ozon", Account: "known-probe-account"}
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Header.Get("Api-Key") == "corrected-synthetic-key" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	if status := providerProbeHTTP(t, gate, scope, server.URL, "wrong-synthetic-key"); status != http.StatusUnauthorized {
		t.Fatalf("wrong probe status %d", status)
	}
	var rejected int
	if err = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM provider_call_rejected_credentials`).Scan(&rejected); err != nil || rejected != 0 {
		t.Fatalf("unversioned probe persisted rejection %d: %v", rejected, err)
	}
	// Historical revision-zero rows must not poison a corrected submission.
	if _, err = db.Pool.Exec(context.Background(), `INSERT INTO provider_call_rejected_credentials(provider,account_id,tenant_id,credential_revision,rejected_at) VALUES($1,$2,$3,0,clock_timestamp())`, scope.Provider, scope.Account, scope.Tenant); err != nil {
		t.Fatal(err)
	}
	if status := providerProbeHTTP(t, gate, scope, server.URL, "corrected-synthetic-key"); status != http.StatusOK {
		t.Fatalf("corrected probe status %d", status)
	}
	var accounts, attempts, completed, interval int64
	var gap float64
	err = db.Pool.QueryRow(context.Background(), `SELECT count(*),sum(attempts),sum(completed),min(interval_ns) FROM provider_call_accounts`).Scan(&accounts, &attempts, &completed, &interval)
	if err != nil || accounts != 1 || attempts != 2 || completed != 2 || interval != int64(time.Second) || received.Load() != 2 {
		t.Fatalf("shared quota accounts=%d attempts=%d completed=%d interval=%d HTTP=%d: %v", accounts, attempts, completed, interval, received.Load(), err)
	}
	err = db.Pool.QueryRow(context.Background(), `SELECT EXTRACT(epoch FROM max(started_at)-min(started_at))::double precision FROM provider_call_permits`).Scan(&gap)
	if err != nil || gap < 0.999 {
		t.Fatalf("corrected submission bypassed account spacing %f: %v", gap, err)
	}
}

func providerProbeHTTP(t testing.TB, gate providercall.Gate, scope providercall.Scope, endpoint, key string) int {
	t.Helper()
	client, err := providercall.Bind(&http.Client{Timeout: 3 * time.Second}, gate, scope, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint+"/auth-probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Api-Key", key)
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("loopback credential probe failed: %v", err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode
}

func TestProviderProbePendingAccountCanCorrectCredentials(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("")
	r.Scope.Provider = "moysklad"
	r.Scope.PendingID = "same-authenticated-probe"
	providerSend(t, gate, r, http.StatusUnauthorized, nil)
	providerSend(t, gate, r, http.StatusOK, nil)
	var accounts, rejected int
	err := db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM provider_call_accounts),(SELECT count(*) FROM provider_call_rejected_credentials)`).Scan(&accounts, &rejected)
	if err != nil || accounts != 1 || rejected != 0 {
		t.Fatalf("pending probe accounts=%d rejections=%d: %v", accounts, rejected, err)
	}
}

func TestProviderProbeStoredCredentialRejectionsRemainFenced(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("stored-probe-account")
	r.Scope.CredentialRevision = 1
	ctx := context.Background()
	if err := db.RegisterStockConnection(ctx, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account, "provider probe test", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'Synthetic',1,true,$4,clock_timestamp(),'provider-probe-test')`, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account, make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	providerSend(t, gate, r, http.StatusUnauthorized, nil)
	if _, err := gate.Begin(ctx, r); !errors.Is(err, providercall.ErrCredentialsRejected) {
		t.Fatalf("stored rejected revision allowed: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE connection_credentials SET revision=2 WHERE tenant_id=$1 AND provider=$2 AND account_id=$3`, r.Scope.Tenant, r.Scope.Provider, r.Scope.Account); err != nil {
		t.Fatal(err)
	}
	r.Scope.CredentialRevision = 2
	providerSend(t, gate, r, http.StatusOK, nil)
	for _, scope := range []providercall.Scope{
		{Tenant: "another-tenant", Provider: r.Scope.Provider, Account: r.Scope.Account, CredentialRevision: 2},
		{Tenant: r.Scope.Tenant, Provider: r.Scope.Provider, Account: r.Scope.Account, CredentialRevision: 1},
		{Tenant: r.Scope.Tenant, Provider: r.Scope.Provider, Account: r.Scope.Account, CredentialRevision: 3},
	} {
		r.Scope = scope
		if _, err := gate.Begin(ctx, r); !errors.Is(err, providercall.ErrCredentialsRejected) {
			t.Fatalf("wrong stored credential scope allowed: %v", err)
		}
	}
}
