//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

func TestProviderCoordinatorSharedAcrossProcesses(t *testing.T) {
	db := catalogSyncDB(t)
	var received, active, maximum atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, current) {
				break
			}
		}
		received.Add(1)
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	providerRunChildren(t, ctx, db.Pool.Config().ConnString(), server.URL)
	if received.Load() != 12 || maximum.Load() > 2 {
		t.Fatalf("received %d, concurrent %d", received.Load(), maximum.Load())
	}
	var attempts, completed int64
	var minimumGap float64
	if err := db.Pool.QueryRow(ctx, `SELECT attempts,completed FROM provider_call_accounts WHERE provider='ozon' AND account_id='shared-process-account'`).Scan(&attempts, &completed); err != nil || attempts != 12 || completed != 12 {
		t.Fatalf("persistent attempts=%d completed=%d: %v", attempts, completed, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT min(EXTRACT(epoch FROM started_at-previous))::double precision FROM (SELECT started_at,lag(started_at) OVER(ORDER BY started_at) previous FROM provider_call_permits WHERE account_id='shared-process-account') p`).Scan(&minimumGap); err != nil || minimumGap < 0.019 {
		t.Fatalf("global request spacing %f: %v", minimumGap, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(DISTINCT tenant_id) FROM provider_call_permits WHERE account_id='shared-process-account'`).Scan(&attempts); err != nil || attempts != 4 {
		t.Fatalf("tenant scopes %d: %v", attempts, err)
	}
}

func providerRunChildren(t *testing.T, ctx context.Context, dsn, endpoint string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 0, 4)
	outputs := make([]*bytes.Buffer, 0, 4)
	for i := range 4 {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProviderCoordinatorProcessChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), "STYK_PROVIDER_CHILD_DSN="+dsn, "STYK_PROVIDER_CHILD_ENDPOINT="+endpoint, fmt.Sprintf("STYK_PROVIDER_CHILD_TENANT=tenant-%d", i))
		output := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = output, output
		if err = cmd.Start(); err != nil {
			t.Error("start provider coordination process")
			break
		}
		commands = append(commands, cmd)
		outputs = append(outputs, output)
	}
	for i, cmd := range commands {
		if err = cmd.Wait(); err != nil {
			t.Errorf("provider coordination child %d: %s", i, outputs[i].String())
		}
	}
}

func TestProviderCoordinatorProcessChild(t *testing.T) {
	dsn := os.Getenv("STYK_PROVIDER_CHILD_DSN")
	if dsn == "" {
		t.Skip("provider coordination subprocess only")
	}
	db, err := postgres.Open(context.Background(), dsn, 5*time.Second)
	if err != nil {
		t.Fatal("open child provider coordination store")
	}
	defer db.Pool.Close()
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond, MaxInflight: 2})
	if err != nil {
		t.Fatal(err)
	}
	r := providerRequest("shared-process-account")
	r.Scope.Tenant = os.Getenv("STYK_PROVIDER_CHILD_TENANT")
	r.Endpoint = os.Getenv("STYK_PROVIDER_CHILD_ENDPOINT")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for range 3 {
		providerChildHTTP(t, ctx, gate, r)
	}
}

func providerChildHTTP(t *testing.T, ctx context.Context, gate *providercall.Coordinator, r providercall.Request) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Endpoint, nil)
	if err != nil {
		t.Fatal("construct loopback fixture request")
	}
	permit, err := gate.Begin(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if releaseErr := permit.Abort(ctx); releaseErr != nil {
			t.Error(releaseErr)
		}
	}()
	if err = permit.Start(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal("loopback fixture HTTP failed")
	}
	if _, err = io.Copy(io.Discard, response.Body); err != nil {
		t.Error("read loopback response")
	}
	if err = response.Body.Close(); err != nil {
		t.Error("close loopback response")
	}
	if err = permit.Finish(ctx, response.StatusCode, response.Header); err != nil {
		t.Fatal(err)
	}
}
