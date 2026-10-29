//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprovider"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

const ordersProcessOrigin = "http://127.0.0.1:15183"

type ordersProcessSettings struct {
	Role, ID, DSN, Provider, Mail, MailCA, Secret, Ready string
	Scopes                                               []orders.Scope
}

// This entry point is re-executed nine times. Every role owns its pool and its
// Go API or application runner; no parent goroutine stands in for a replica.
func TestOrdersProcessChild(t *testing.T) {
	path := os.Getenv("STYK_ORDERS_PROCESS_SETTINGS")
	if path == "" {
		t.Skip("orders process subprocess only")
	}
	settings := ordersProcessReadSettings(t, path)
	ordersProcessSafety(t, settings.DSN)
	db, err := postgres.Open(context.Background(), settings.DSN, 4*time.Second)
	if err != nil {
		t.Fatal("open isolated orders process store")
	}
	defer db.Pool.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	control := &ordersProcessControl{Store: db, secret: settings.Secret}
	handler := ordersProcessChildHandler(t, db, settings, control)
	server := ordersProcessServe(t, handler, settings.Ready)
	defer server.Close()
	if settings.Role == "api" {
		<-ctx.Done()
		return
	}
	ordersProcessRun(t, ctx, control, settings)
}

func ordersProcessReadSettings(t *testing.T, path string) ordersProcessSettings {
	t.Helper()
	raw, err := os.ReadFile(path)
	var settings ordersProcessSettings
	if err != nil || json.Unmarshal(raw, &settings) != nil {
		t.Fatal("read synthetic orders process settings")
	}
	if settings.Secret == "" || !ordersProcessLoopback(settings.Provider) {
		t.Fatal("orders process requires protected literal loopback fixtures")
	}
	return settings
}

func ordersProcessChildHandler(t *testing.T, db *postgres.Store, s ordersProcessSettings, c *ordersProcessControl) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__test/state", c.state)
	mux.HandleFunc("POST /__test/control", c.configure)
	mux.HandleFunc("POST /__test/stale-ack", c.staleAck)
	if s.Role != "api" {
		return mux
	}
	sender, err := identity.NewSMTP(s.Mail, "orders-fixture@example.test", "", "")
	if err != nil {
		t.Fatal("construct loopback SMTP sender")
	}
	handler, err := console.MerchantLoopbackTestHandler(db, ordersProcessOrigin, sender, s.Provider, s.Provider)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", handler)
	return mux
}

func ordersProcessServe(t *testing.T, handler http.Handler, ready string) *http.Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listen on loopback process address")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error("close orders process HTTP server")
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error("orders process HTTP server failed")
		}
	})
	if err = os.WriteFile(ready, []byte("http://"+listener.Addr().String()), 0o600); err != nil {
		t.Fatal("publish orders child readiness")
	}
	return server
}

func ordersProcessFactory(t *testing.T, db *postgres.Store, base string) orderprovider.Factory {
	t.Helper()
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		t.Fatal("synthetic orders vault unavailable")
	}
	gate, err := providercall.New(db, providercall.Options{SyntheticInterval: 20 * time.Millisecond, MaxInflight: 2})
	if err != nil {
		t.Fatal(err)
	}
	return orderprovider.Factory{Credentials: &credentials.Resolver{Store: db, Vault: vault}, Gate: gate, DeliveryObserver: db,
		NewMoysklad: func(_, secret string) (*moysklad.Client, error) {
			return moysklad.New(moysklad.Options{BaseURL: base, Token: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 25})
		}, NewOzon: func(account, secret string) (*ozon.Client, error) {
			return ozon.New(ozon.Options{BaseURL: base, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 25})
		}}
}

func ordersProcessRun(t *testing.T, ctx context.Context, store *ordersProcessControl, settings ordersProcessSettings) {
	t.Helper()
	factory := ordersProcessFactory(t, store.Store, settings.Provider)
	allow := func(scope orders.Scope) bool {
		for _, allowed := range settings.Scopes {
			if scope == allowed {
				return true
			}
		}
		return false
	}
	service, err := orderflow.NewService(store.Store, factory, orderflow.ServiceOptions{AllowScope: allow})
	if err != nil {
		t.Fatal(err)
	}
	collector := ordersProcessCollector{db: store.Store, factory: factory, worker: settings.ID}
	resolver := ordersProcessObservedResolver{factory: factory, control: store}
	runner, err := orderflow.NewRunner(store, resolver, orderflow.RunnerOptions{WorkerID: settings.ID, AllowScope: allow, Service: service, Collector: collector, LeaseDuration: 5 * time.Second, PollInterval: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ordersProcessLoop(ctx, store, settings.Role, runner)
}

func ordersProcessLoop(ctx context.Context, control *ordersProcessControl, role string, runner *orderflow.Runner) {
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		if control.enabled() {
			var err error
			if role == "scheduler" {
				err = runner.Tick(ctx)
			} else {
				_, err = runner.RunNext(ctx)
			}
			control.result(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

type ordersProcessCollector struct {
	db      *postgres.Store
	factory orderprovider.Factory
	worker  string
}

func (c ordersProcessCollector) Collect(ctx context.Context, scope orders.Scope) error {
	binding, err := c.factory.ResolveOrder(ctx, scope, "ozon", scope.OzonAccount, 0)
	if err != nil {
		return err
	}
	source, ok := binding.Client.(orderflow.SourceReader)
	if !ok {
		return orders.ErrBlocked
	}
	collector, err := orderflow.NewIngestor(c.db, source, orderflow.IngestionOptions{WorkerID: c.worker, LeaseDuration: 5 * time.Second, PageSize: 25})
	if err != nil {
		return err
	}
	return collector.Collect(ctx, scope)
}

// Fault barriers surround real durable store calls. They never synthesize work,
// provider receipts or acknowledgements.
type ordersProcessControl struct {
	*postgres.Store
	mu          sync.Mutex
	secret      string
	on          bool
	barrier     string
	reached     string
	release     chan struct{}
	last        *orders.Work
	claims      int
	errors      int
	guardDelay  time.Duration
	sourceError string
}

func (c *ordersProcessControl) authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Test-Secret") != c.secret {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	return true
}

func (c *ordersProcessControl) configure(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(w, r) {
		return
	}
	var body struct {
		Enabled      bool   `json:"enabled"`
		Barrier      string `json:"barrier"`
		Release      bool   `json:"release"`
		GuardDelayMS *int   `json:"guard_delay_ms"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.on, c.barrier = body.Enabled, body.Barrier
	if body.GuardDelayMS != nil && *body.GuardDelayMS >= 0 && *body.GuardDelayMS <= 500 {
		c.guardDelay = time.Duration(*body.GuardDelayMS) * time.Millisecond
	}
	if body.Release && c.release != nil {
		close(c.release)
		c.release = nil
		c.reached = ""
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ordersProcessControl) state(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(w, r) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := json.NewEncoder(w).Encode(map[string]any{"pid": os.Getpid(), "claims": c.claims, "errors": c.errors, "reached": c.reached, "source_error": c.sourceError}); err != nil {
		return
	}
}

type ordersProcessObservedResolver struct {
	factory orderprovider.Factory
	control *ordersProcessControl
}

func (r ordersProcessObservedResolver) ResolveOrder(ctx context.Context, scope orders.Scope, provider, account string, revision int64) (orders.Binding, error) {
	binding, err := r.factory.ResolveOrder(ctx, scope, provider, account, revision)
	if err == nil && provider == "ozon" {
		binding.Client = ordersProcessObservedProvider{Provider: binding.Client, control: r.control}
	}
	return binding, err
}

type ordersProcessObservedProvider struct {
	orders.Provider
	control *ordersProcessControl
}

func (p ordersProcessObservedProvider) ReadOrderPosting(ctx context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	posting, err := p.Provider.ReadOrderPosting(ctx, scope, number)
	if err != nil {
		p.control.mu.Lock()
		p.control.sourceError = err.Error()
		p.control.mu.Unlock()
	}
	return posting, err
}

func (c *ordersProcessControl) enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.on
}

func (c *ordersProcessControl) result(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.errors++
	c.mu.Unlock()
}

func (c *ordersProcessControl) wait(ctx context.Context, phase string) {
	c.mu.Lock()
	if c.barrier != phase {
		c.mu.Unlock()
		return
	}
	c.reached, c.release = phase, make(chan struct{})
	release := c.release
	c.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-release:
	}
}

func (c *ordersProcessControl) ClaimOrderWork(ctx context.Context, request orders.ClaimRequest) (*orders.Work, error) {
	c.wait(ctx, "before-claim")
	work, err := c.Store.ClaimOrderWork(ctx, request)
	if err != nil || work == nil {
		return work, err
	}
	c.mu.Lock()
	c.claims++
	c.last = work
	c.mu.Unlock()
	phase := "after-claim"
	if work.State.ReadOnly() {
		phase = "during-poll"
	}
	c.wait(ctx, phase)
	return work, nil
}

func (c *ordersProcessControl) CompleteOrderWork(ctx context.Context, completion orders.Completion) error {
	if completion.ExpectedState == orders.StateDispatching {
		c.wait(ctx, "after-provider-before-ack")
	}
	return c.Store.CompleteOrderWork(ctx, completion)
}

func (c *ordersProcessControl) GuardOrderDispatch(ctx context.Context, guard orders.DispatchGuard) (bool, error) {
	allowed, err := c.Store.GuardOrderDispatch(ctx, guard)
	if !allowed || err != nil || !guard.Begin {
		return allowed, err
	}
	c.mu.Lock()
	delay := c.guardDelay
	c.mu.Unlock()
	if delay == 0 {
		return true, nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		return true, nil
	}
}

func (c *ordersProcessControl) staleAck(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(w, r) {
		return
	}
	c.mu.Lock()
	work := c.last
	c.mu.Unlock()
	if work == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	err := c.Store.CompleteOrderWork(r.Context(), orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: work.State, State: orders.StateBlocked, Code: "STALE_CHILD_MUST_NOT_ACK"})
	if errors.Is(err, orders.ErrStale) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
