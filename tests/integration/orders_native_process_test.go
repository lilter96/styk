//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orderprivacy"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

// This is a subprocess entry point, not a replacement worker or queue. It uses
// the same durable store, scoped adapters, action protector and normal Runner.
func TestOrdersNativeProcessChild(t *testing.T) {
	path := os.Getenv("STYK_ORDERS_NATIVE_PROCESS_SETTINGS")
	if path == "" {
		t.Skip("native worker subprocess only")
	}
	settings := ordersProcessReadSettings(t, path)
	ordersProcessSafety(t, settings.DSN)
	db, err := postgres.Open(context.Background(), settings.DSN, 4*time.Second)
	if err != nil {
		t.Fatal("open isolated native worker store")
	}
	defer db.Pool.Close()
	if db.Pool.Config().MaxConns != 4 {
		t.Fatal("native child requires bounded four-connection pool")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	control := &ordersProcessControl{Store: db, secret: settings.Secret}
	ordersProcessServe(t, ordersProcessChildHandler(t, db, settings, control), settings.Ready)
	nativeProcessRun(t, ctx, control, settings)
}

func nativeProcessRun(t *testing.T, ctx context.Context, store *ordersProcessControl, settings ordersProcessSettings) {
	t.Helper()
	factory := ordersProcessFactory(t, store.Store, settings.Provider)
	allow := func(scope orders.Scope) bool {
		for _, candidate := range settings.Scopes {
			if candidate == scope {
				return true
			}
		}
		return false
	}
	protector := orderprivacy.ActionProtector{Vault: factory.Credentials.(*credentials.Resolver).Vault, Store: store.Store}
	service, err := orderflow.NewService(store.Store, factory, orderflow.ServiceOptions{AllowScope: allow, ActionProtector: protector})
	if err != nil {
		t.Fatal(err)
	}
	collector := ordersProcessCollector{db: store.Store, factory: factory, worker: settings.ID}
	runner, err := orderflow.NewRunner(store, factory, orderflow.RunnerOptions{WorkerID: settings.ID, AllowScope: allow, Service: service, Collector: collector, ActionReader: protector, LeaseDuration: 15 * time.Second, PollInterval: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ordersProcessLoop(ctx, store, "worker", runner)
}

func nativeProcessStart(t *testing.T, settings ordersProcessSettings) *ordersProcessChild {
	t.Helper()
	dir := t.TempDir()
	settings.Ready = filepath.Join(dir, "ready")
	path := filepath.Join(dir, "settings.json")
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal("encode private native worker settings")
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal("write private native worker settings")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := &ordersProcessChild{settings: settings, done: make(chan error, 1), logs: &ordersProcessLog{}}
	child.cmd = exec.Command(executable, "-test.run=^TestOrdersNativeProcessChild$", "-test.count=1")
	child.cmd.Env = append(os.Environ(), "STYK_ORDERS_NATIVE_PROCESS_SETTINGS="+path)
	child.cmd.Stdout, child.cmd.Stderr = child.logs, child.logs
	if err = child.cmd.Start(); err != nil {
		t.Fatal("start independent native worker process")
	}
	go func() { child.done <- child.cmd.Wait() }()
	t.Cleanup(func() { child.stop(t, false) })
	ordersProcessEventually(t, 10*time.Second, "native worker readiness", func() bool {
		ready, err := os.ReadFile(settings.Ready)
		child.url = string(ready)
		return err == nil && ordersProcessLoopback(child.url)
	})
	return child
}

type nativeProcessStack struct {
	*ordersProcessStack
	native *nativeMovementHTTPProvider
	hidden atomic.Bool
}

func newNativeProcessStack(t *testing.T) *nativeProcessStack {
	t.Helper()
	db, vault := ordersProcessDB(t)
	base := ordersProcessNewProvider(t)
	position := &positionHTTPProvider{base: base}
	position.quantity.Store(2)
	position.price.Store(1234)
	position.revision.Store(1)
	native := &nativeMovementHTTPProvider{position: position, documents: map[string]*nativeMovementHTTPDocument{}}
	stack := &nativeProcessStack{ordersProcessStack: &ordersProcessStack{owner: t, db: db, vault: vault, provider: base}, native: native}
	base.server.Config.Handler = http.HandlerFunc(stack.serve)
	mailbox := &testMailbox{tokens: map[string]string{}}
	mail, ca := ordersProcessMail(t, mailbox)
	u, err := url.Parse(db.Pool.Config().ConnString())
	if err != nil {
		t.Fatal("parse native test database")
	}
	query := u.Query()
	query.Set("pool_max_conns", "4")
	u.RawQuery = query.Encode()
	stack.settings = ordersProcessSettings{DSN: u.String(), Provider: base.server.URL, Mail: mail, MailCA: ca, Secret: ordersProcessRandom(t)}
	for index := range 3 {
		stack.apis = append(stack.apis, stack.start(t, "api", index))
	}
	stack.a, stack.b = ordersHTTPNewBrowser(t, stack.apis), ordersHTTPNewBrowser(t, stack.apis)
	a := stack.a.register(t, mailbox, "native-process-a@example.test")
	b := stack.b.register(t, mailbox, "native-process-b@example.test")
	stack.a.scope = ordersProcessSeed(t, db, vault, a.AccountID, 1)
	stack.b.scope = ordersProcessSeed(t, db, vault, b.AccountID, 9)
	stack.settings.Scopes = []orders.Scope{stack.a.scope, stack.b.scope}
	config := ordersProcessConfig(stack.a.scope)
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: "constant.zero", Target: "positions.reserve"})
	config.NativeDocuments = &orders.NativeDocumentPolicy{Enabled: true, Actions: []orders.Action{orders.ActionDemandCreate}, MaximumQuantity: "5", EventMaxAgeSeconds: 3600}
	stack.a.saveConfig(t, config)
	stack.b.saveConfig(t, ordersProcessConfig(stack.b.scope))
	for index := range 2 {
		stack.schedules = append(stack.schedules, stack.start(t, "scheduler", index))
	}
	for index := range 4 {
		stack.workers = append(stack.workers, stack.nativeWorker(t, index))
	}
	for _, scheduler := range stack.schedules {
		scheduler.configure(t, true, "", false)
	}
	return stack
}

func (s *nativeProcessStack) nativeWorker(t *testing.T, index int) *ordersProcessChild {
	t.Helper()
	settings := s.settings
	settings.Role = "worker"
	settings.ID = fmt.Sprintf("native-worker-%d", index)
	return nativeProcessStart(s.owner, settings)
}

func (s *nativeProcessStack) serve(w http.ResponseWriter, r *http.Request) {
	if s.hidden.Load() && s.native.writes.Load() > 0 && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/entity/demand") {
		if r.URL.Path == "/entity/demand" {
			s.provider.page(w, []any{}, 0, 2, 0)
		} else {
			w.WriteHeader(404)
		}
		return
	}
	s.native.serve(w, r)
}

func (s *nativeProcessStack) nativePreview(t *testing.T, event string) orders.Preview {
	t.Helper()
	source := s.a.preview(t, "position-http-posting").Decisions[0].Posting
	proposal := orders.NativeMovementProposal{PostingNumber: source.PostingNumber, Action: orders.ActionDemandCreate, Applicable: orders.Present(false), Lines: []orders.ActionQuantity{{Identity: source.Lines[0].Identity, Quantity: "1"}}, EventKind: "draft_plan", PlannedMoment: time.Now().UTC().Add(time.Hour), EventReference: event}
	status, raw := s.a.request(t, 1, http.MethodPost, "/api/orders/preview", map[string]any{"tenant": s.a.scope.Tenant, "moysklad_account": s.a.scope.MoyskladAccount, "ozon_account": s.a.scope.OzonAccount, "expected_revision": s.a.config.Revision, "posting_numbers": []string{source.PostingNumber}, "native_proposals": []orders.NativeMovementProposal{proposal}}, true)
	var preview orders.Preview
	if status != 200 || json.Unmarshal(raw, &preview) != nil || len(preview.Decisions) != 1 {
		t.Fatalf("native process preview HTTP %d", status)
	}
	if !preview.Decisions[0].Ready {
		t.Fatalf("native process preview blocked: %+v", preview.Decisions[0].Validation)
	}
	return preview
}

func TestOrdersNativeNineProcessStack(t *testing.T) {
	if os.Getenv("STYK_PROCESS_E2E") != "true" {
		t.Skip("set STYK_PROCESS_E2E=true for independent native-process acceptance")
	}
	s := newNativeProcessStack(t)
	pids := map[int]bool{}
	for _, group := range [][]*ordersProcessChild{s.apis, s.schedules, s.workers} {
		for _, child := range group {
			pids[child.cmd.Process.Pid] = true
		}
	}
	if len(pids) != 9 {
		t.Fatal("native topology must have nine independent OS processes")
	}
	s.add(t, s.a, "position-http-posting", "FBS", false)
	initial := s.a.preview(t, "position-http-posting")
	s.approveAcrossReplicas(t, initial)
	s.workersEnabled(t, true)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
	draft := s.nativePreview(t, "process-native-first-draft")
	ordersHTTPIsolation(t, s.a, s.b, draft.ID)
	s.nativeApprove(t, draft, "process-native-first-draft")
	s.workersEnabled(t, true)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
	if s.native.writes.Load() != 1 {
		t.Fatal("concurrent workers duplicated initial native draft")
	}
	if !t.Run("lost-ack-stays-unknown-without-repeat", s.nativeUnknown) {
		return
	}
	if !t.Run("kill-after-native-post-before-database-ack", s.nativeCrash) {
		return
	}
	t.Run("late-native-claimant-cannot-ack-or-post", s.nativeStaleClaim)
	t.Logf("nine process synthetic HTTP requests=%d native POST attempts=%d", s.native.requests.Load(), s.native.writes.Load())
}

func (s *nativeProcessStack) nativeUnknown(t *testing.T) {
	preview := s.nativePreview(t, "process-native-lost-ack")
	s.nativeApprove(t, preview, "process-native-lost-ack")
	s.hidden.Store(true)
	s.native.loseResponse.Store(true)
	s.workersEnabled(t, true)
	s.awaitState(t, "position-http-posting", orders.StateUnknown)
	time.Sleep(500 * time.Millisecond)
	if s.native.writes.Load() != 2 {
		t.Fatal("UNKNOWN native creation was automatically resent")
	}
	s.hidden.Store(false)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
	if s.native.writes.Load() != 2 {
		t.Fatal("native readback recovery issued a new POST")
	}
}

func (s *nativeProcessStack) nativeCrash(t *testing.T) {
	preview := s.nativePreview(t, "process-native-kill-after-write")
	s.nativeApprove(t, preview, "process-native-kill-after-write")
	worker := s.workers[0]
	s.faultWorker(t, worker, "after-provider-before-ack")
	s.awaitBarrier(t, worker, "after-provider-before-ack")
	if s.native.writes.Load() != 3 {
		t.Fatal("fault point did not follow actual native provider POST")
	}
	worker.stop(t, true)
	s.workers[0] = s.nativeWorker(t, 0)
	s.workersEnabled(t, true)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	s.workersEnabled(t, false)
	if s.native.writes.Load() != 3 {
		t.Fatal("killed native worker caused duplicate provider creation")
	}
	var count int
	if err := s.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders_native_document_results`).Scan(&count); err != nil || count != 3 {
		t.Fatal("native process reconciliation history incomplete")
	}
}

func (s *nativeProcessStack) nativeApprove(t *testing.T, preview orders.Preview, event string) {
	t.Helper()
	rival := s.nativePreview(t, event)
	results := s.concurrentApprovals(t, []string{preview.ID, preview.ID, preview.ID, rival.ID})
	var operation orders.Operation
	winner := ""
	for range 4 {
		result := <-results
		if result.status == http.StatusConflict {
			continue
		}
		var candidate orders.Operation
		if result.status != 200 || json.Unmarshal(result.raw, &candidate) != nil || candidate.ID == "" {
			t.Fatalf("native concurrent API approval HTTP %d", result.status)
		}
		if operation.ID != "" && (operation.ID != candidate.ID || winner != result.preview) {
			t.Fatal("native concurrent previews created multiple durable operations")
		}
		operation, winner = candidate, result.preview
	}
	if winner == "" {
		t.Fatal("all concurrent native approvals rejected")
	}
	s.replayApproval(t, winner, operation.ID)
}

func (s *nativeProcessStack) nativeStaleClaim(t *testing.T) {
	preview := s.nativePreview(t, "process-native-expired-claim")
	s.nativeApprove(t, preview, "process-native-expired-claim")
	old := s.workers[0]
	s.faultWorker(t, old, "after-claim")
	s.awaitBarrier(t, old, "after-claim")
	time.Sleep(2100 * time.Millisecond)
	s.workers[1].configure(t, true, "", true)
	s.awaitState(t, "position-http-posting", orders.StateReconciled)
	if status, _ := old.request(t, http.MethodPost, "/__test/stale-ack", map[string]any{}); status != http.StatusConflict {
		t.Fatalf("old native claimant acknowledged new owner HTTP %d", status)
	}
	old.configure(t, false, "", true)
	s.workersEnabled(t, false)
	if s.native.writes.Load() != 4 {
		t.Fatal("late native claimant duplicated a provider creation")
	}
}
