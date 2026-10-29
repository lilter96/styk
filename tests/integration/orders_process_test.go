//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

type ordersProcessStack struct {
	owner     *testing.T
	db        *postgres.Store
	vault     *credentials.Vault
	provider  *ordersProcessProvider
	settings  ordersProcessSettings
	apis      []*ordersProcessChild
	schedules []*ordersProcessChild
	workers   []*ordersProcessChild
	a, b      *ordersHTTPBrowser
}

func TestOrdersNineProcessStack(t *testing.T) {
	stack := ordersProcessNewStack(t)
	if !t.Run("sessions-and-isolation", stack.sessionsAndIsolation) || !t.Run("provider-global-cooldown", stack.cooldown) || !t.Run("concurrent-approval-and-workers", stack.concurrentApproval) || !t.Run("unknown-does-not-resend-or-block-other-postings", stack.unknownOutcome) {
		return
	}
	for _, phase := range []string{"before-claim", "after-claim", "after-provider-before-ack", "during-poll"} {
		if !t.Run("kill-restart-"+phase, func(t *testing.T) { stack.crashRecovery(t, phase) }) {
			return
		}
	}
	t.Run("stale-child-cannot-ack", stack.staleChild)
	t.Run("browser", stack.browser)
	t.Run("credential-rotation-and-expired-grant", stack.authorityChanges)
	stack.provider.assertBudget(t)
}

func ordersProcessNewStack(t *testing.T) *ordersProcessStack {
	t.Helper()
	db, vault := ordersProcessDB(t)
	stack := &ordersProcessStack{owner: t, db: db, vault: vault, provider: ordersProcessNewProvider(t)}
	mailbox := &testMailbox{tokens: make(map[string]string)}
	mail, ca := ordersProcessMail(t, mailbox)
	u, err := url.Parse(db.Pool.Config().ConnString())
	if err != nil {
		t.Fatal("parse isolated process database")
	}
	query := u.Query()
	query.Set("pool_max_conns", "4")
	u.RawQuery = query.Encode()
	stack.settings = ordersProcessSettings{DSN: u.String(), Provider: stack.provider.server.URL, Mail: mail, MailCA: ca, Secret: ordersProcessRandom(t)}
	for i := range 3 {
		stack.apis = append(stack.apis, stack.start(t, "api", i))
	}
	stack.a, stack.b = ordersHTTPNewBrowser(t, stack.apis), ordersHTTPNewBrowser(t, stack.apis)
	userA := stack.a.register(t, mailbox, "orders-process-a@example.test")
	userB := stack.b.register(t, mailbox, "orders-process-b@example.test")
	stack.a.scope = ordersProcessSeed(t, db, vault, userA.AccountID, 1)
	stack.b.scope = ordersProcessSeed(t, db, vault, userB.AccountID, 9)
	stack.settings.Scopes = []orders.Scope{stack.a.scope, stack.b.scope}
	stack.a.saveConfig(t, ordersProcessConfig(stack.a.scope))
	stack.b.saveConfig(t, ordersProcessConfig(stack.b.scope))
	stack.startRunners(t)
	return stack
}

func (s *ordersProcessStack) start(t *testing.T, role string, index int) *ordersProcessChild {
	t.Helper()
	settings := s.settings
	settings.Role, settings.ID = role, fmt.Sprintf("orders-%s-%d", role, index)
	return ordersProcessStart(s.owner, settings)
}

func (s *ordersProcessStack) startRunners(t *testing.T) {
	t.Helper()
	for i := range 2 {
		s.schedules = append(s.schedules, s.start(t, "scheduler", i))
	}
	for i := range 4 {
		s.workers = append(s.workers, s.start(t, "worker", i))
	}
	pids := make(map[int]bool)
	for _, child := range append(append(append([]*ordersProcessChild{}, s.apis...), s.schedules...), s.workers...) {
		if pids[child.cmd.Process.Pid] {
			t.Fatal("orders replicas must have independent OS process IDs")
		}
		pids[child.cmd.Process.Pid] = true
	}
	if len(pids) != 9 {
		t.Fatalf("orders process topology has %d processes", len(pids))
	}
	for _, child := range s.schedules {
		child.configure(t, true, "", false)
	}
}

func (s *ordersProcessStack) sessionsAndIsolation(t *testing.T) {
	old := ordersHTTPNewBrowser(t, s.apis)
	u, err := url.Parse(s.apis[0].url)
	if err != nil {
		t.Fatal(err)
	}
	old.client.Jar.SetCookies(u, s.a.client.Jar.Cookies(u))
	old.csrf = s.a.csrf
	if status, _ := s.a.request(t, 1, http.MethodDelete, "/api/session", map[string]any{}, true); status != http.StatusNoContent {
		t.Fatalf("logout through API 2: %d", status)
	}
	for replica := range s.apis {
		if status, _ := old.request(t, replica, http.MethodGet, "/api/session", nil, false); status != http.StatusUnauthorized {
			t.Fatalf("revoked session survived on API %d: %d", replica+1, status)
		}
	}
	s.a.login(t, "orders-process-a@example.test")
	s.add(t, s.a, "isolation-ready", "FBS", false)
	preview := s.a.preview(t, "isolation-ready")
	ordersHTTPIsolation(t, s.a, s.b, preview.ID)
	s.add(t, s.a, "fbo-excluded", "FBO", false)
	fbo := s.a.preview(t, "fbo-excluded")
	if len(fbo.Decisions) != 1 || fbo.Decisions[0].Ready || len(fbo.Decisions[0].Commands) != 0 {
		t.Fatal("FBO facts produced a write command")
	}
}

func (s *ordersProcessStack) add(t *testing.T, browser *ordersHTTPBrowser, number, scheme string, lost bool) {
	t.Helper()
	s.provider.add(browser.scope.OzonAccount, number, scheme, lost)
	browser.check(t)
	nextCheck := time.Now().Add(500 * time.Millisecond)
	ordersProcessEventually(t, 15*time.Second, "cross-process scheduler ingestion of "+number, func() bool {
		if time.Now().After(nextCheck) {
			browser.check(t)
			nextCheck = time.Now().Add(500 * time.Millisecond)
		}
		var exists bool
		err := s.db.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM orders_posting_heads WHERE tenant_id=$1 AND posting_number=$2)`, browser.scope.Tenant, number).Scan(&exists)
		return err == nil && exists
	})
}

func (s *ordersProcessStack) workersEnabled(t *testing.T, enabled bool) {
	t.Helper()
	for _, worker := range s.workers {
		worker.configure(t, enabled, "", true)
	}
}

func (s *ordersProcessStack) awaitState(t *testing.T, number string, wanted orders.State) {
	t.Helper()
	var state, code string
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last durable order phase for %s: state=%s code=%s", number, state, code)
		}
	})
	ordersProcessEventually(t, 20*time.Second, "durable "+string(wanted)+" for "+number, func() bool {
		err := s.db.Pool.QueryRow(context.Background(), `SELECT state,COALESCE(work->>'code','') FROM orders_work WHERE work->'command'->>'posting_number'=$1 ORDER BY created_at DESC LIMIT 1`, number).Scan(&state, &code)
		if err == nil && (state == string(orders.StateBlocked) || state == string(orders.StateRejected)) && state != string(wanted) {
			if code == "FRESH_SOURCE_CHANGED_OR_UNAVAILABLE" {
				s.reportSource(t, number)
			}
			t.Fatalf("order stopped before expected phase: state=%s code=%s", state, code)
		}
		return err == nil && state == string(wanted)
	})
}

func (s *ordersProcessStack) concurrentApproval(t *testing.T) {
	numbers := make([]string, 0, 8)
	for index := range 8 {
		number := ordersProcessNumber("concurrent", index)
		s.provider.add(s.a.scope.OzonAccount, number, "FBS", false)
		numbers = append(numbers, number)
	}
	s.add(t, s.a, numbers[0], "FBS", false)
	ordersProcessEventually(t, 15*time.Second, "all concurrent posting facts", func() bool {
		var count int
		err := s.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders_posting_heads WHERE posting_number LIKE 'concurrent-%'`).Scan(&count)
		return err == nil && count == len(numbers)
	})
	preview := s.a.preview(t, numbers...)
	ordersProcessAssertReady(t, preview, len(numbers))
	operation := s.approveAcrossReplicas(t, preview)
	status, raw := s.a.request(t, 2, http.MethodGet, "/api/orders/operations"+ordersHTTPQuery(s.a.scope), nil, false)
	var history orders.HistoryPage
	if status != http.StatusOK || json.Unmarshal(raw, &history) != nil || len(history.Operations) != 1 || history.Operations[0].ID != operation.ID {
		t.Fatal("API 3 did not observe the operation accepted through API 1")
	}
	for _, worker := range s.workers {
		status, _ := worker.request(t, http.MethodPost, "/__test/control", map[string]any{"enabled": true, "guard_delay_ms": 100})
		if status != http.StatusNoContent {
			t.Fatal("configure final database guard latency regression")
		}
	}
	for _, number := range numbers {
		s.awaitState(t, number, orders.StateReconciled)
		if s.provider.writeCount(number) != 1 {
			t.Fatal("concurrent approval duplicated a native customerorder")
		}
	}
	s.assertWorkersClaimed(t)
	s.workersEnabled(t, false)
}

func (s *ordersProcessStack) cooldown(t *testing.T) {
	s.provider.mu.Lock()
	s.provider.limited["moysklad:"+s.a.scope.MoyskladAccount] = true
	s.provider.mu.Unlock()
	results := make(chan int, 3)
	for replica := range 3 {
		go func() {
			status, _ := s.a.request(t, replica, http.MethodGet, "/api/orders/metadata"+ordersHTTPQuery(s.a.scope), nil, false)
			results <- status
		}()
	}
	successes, limited := 0, 0
	for range 3 {
		status := <-results
		if status == http.StatusOK {
			successes++
		}
		if status == http.StatusServiceUnavailable {
			limited++
		}
	}
	if successes != 2 || limited != 1 {
		t.Fatalf("cross-process rate limit results: success=%d limited=%d", successes, limited)
	}
}

func ordersProcessAssertReady(t *testing.T, preview orders.Preview, count int) {
	t.Helper()
	if len(preview.Decisions) != count {
		t.Fatalf("preview decisions=%d expected=%d", len(preview.Decisions), count)
	}
	for _, decision := range preview.Decisions {
		if !decision.Ready || len(decision.Commands) != 1 {
			t.Fatalf("synthetic posting not ready: validation=%v", decision.Validation)
		}
	}
}

func (s *ordersProcessStack) approveAcrossReplicas(t *testing.T, preview orders.Preview) orders.Operation {
	t.Helper()
	type result struct {
		status int
		raw    []byte
	}
	results := make(chan result, 3)
	body := ordersHTTPBody(s.a.scope)
	body["preview_id"], body["idempotency_key"], body["acknowledge"] = preview.ID, "same-approval-"+preview.ID, true
	for replica := range 3 {
		go func() {
			status, raw := s.a.request(t, replica, http.MethodPost, "/api/orders/approve", body, true)
			results <- result{status, raw}
		}()
	}
	var operation orders.Operation
	for range 3 {
		result := <-results
		var candidate orders.Operation
		if result.status != http.StatusOK || json.Unmarshal(result.raw, &candidate) != nil || candidate.ID == "" {
			t.Fatalf("concurrent cross-API approval: %d", result.status)
		}
		if operation.ID != "" && operation.ID != candidate.ID {
			t.Fatal("idempotent approval created separate operations across APIs")
		}
		operation = candidate
	}
	return operation
}

func (s *ordersProcessStack) assertWorkersClaimed(t *testing.T) {
	t.Helper()
	claimed := 0
	for _, worker := range s.workers {
		_, raw := worker.request(t, http.MethodGet, "/__test/state", nil)
		var state struct{ Claims int }
		if json.Unmarshal(raw, &state) != nil {
			t.Fatal("decode process claim evidence")
		}
		if state.Claims > 0 {
			claimed++
		}
	}
	if claimed < 2 {
		t.Fatalf("only %d independent workers claimed durable order tasks", claimed)
	}
	var intents int
	if err := s.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders_write_intents`).Scan(&intents); err != nil || intents != 8 {
		t.Fatalf("concurrent approvals produced %d durable write intents: %v", intents, err)
	}
}

func (s *ordersProcessStack) unknownOutcome(t *testing.T) {
	s.add(t, s.a, "unknown-lost", "FBS", true)
	s.add(t, s.b, "unaffected-merchant", "FBS", false)
	s.a.approve(t, s.a.preview(t, "unknown-lost"))
	s.b.approve(t, s.b.preview(t, "unaffected-merchant"))
	s.workersEnabled(t, true)
	s.awaitState(t, "unknown-lost", orders.StateUnknown)
	s.awaitState(t, "unaffected-merchant", orders.StateReconciled)
	time.Sleep(450 * time.Millisecond)
	if s.provider.writeCount("unknown-lost") != 1 {
		t.Fatal("unknown provider outcome was automatically resent")
	}
	preview := s.a.preview(t, "unknown-lost")
	if len(preview.Decisions) != 1 || preview.Decisions[0].Ready {
		t.Fatal("unknown target did not retain the per-posting fence")
	}
	s.provider.reveal("unknown-lost")
	s.awaitState(t, "unknown-lost", orders.StateReconciled)
	s.workersEnabled(t, false)
}

func (s *ordersProcessStack) crashRecovery(t *testing.T, phase string) {
	s.workersEnabled(t, false)
	number := "crash-" + phase
	s.add(t, s.a, number, "FBS", phase == "during-poll")
	s.a.approve(t, s.a.preview(t, number))
	worker := s.workers[0]
	worker.configure(t, true, phase, true)
	s.awaitBarrier(t, worker, phase)
	worker.stop(t, true)
	s.workers[0] = s.start(t, "worker", 0)
	if phase == "during-poll" {
		s.provider.reveal(number)
	}
	s.workersEnabled(t, true)
	s.awaitState(t, number, orders.StateReconciled)
	if s.provider.writeCount(number) != 1 {
		t.Fatalf("kill/restart at %s duplicated or lost provider creation", phase)
	}
	s.workersEnabled(t, false)
}

func (s *ordersProcessStack) awaitBarrier(t *testing.T, worker *ordersProcessChild, phase string) {
	t.Helper()
	ordersProcessEventually(t, 20*time.Second, "real child fault barrier "+phase, func() bool {
		status, raw := worker.request(t, http.MethodGet, "/__test/state", nil)
		var state struct{ Reached string }
		return status == http.StatusOK && json.Unmarshal(raw, &state) == nil && state.Reached == phase
	})
}

func (s *ordersProcessStack) staleChild(t *testing.T) {
	s.add(t, s.a, "stale-child", "FBS", false)
	s.a.approve(t, s.a.preview(t, "stale-child"))
	old := s.workers[0]
	old.configure(t, true, "after-claim", true)
	s.awaitBarrier(t, old, "after-claim")
	time.Sleep(5100 * time.Millisecond)
	s.workers[1].configure(t, true, "", true)
	s.awaitState(t, "stale-child", orders.StateReconciled)
	if status, _ := old.request(t, http.MethodPost, "/__test/stale-ack", map[string]any{}); status != http.StatusConflict {
		t.Fatalf("stale child acknowledged another claim: %d", status)
	}
	old.configure(t, false, "", true)
	s.workersEnabled(t, false)
	if s.provider.writeCount("stale-child") != 1 {
		t.Fatal("stale claimant duplicated provider write")
	}
}

func (s *ordersProcessStack) authorityChanges(t *testing.T) {
	s.add(t, s.a, "credential-rotated", "FBS", false)
	s.a.approve(t, s.a.preview(t, "credential-rotated"))
	ordersProcessCredential(t, s.db, s.vault, credentials.Scope{Tenant: s.a.scope.Tenant, Provider: "moysklad", Account: s.a.scope.MoyskladAccount}, 1)
	s.workersEnabled(t, true)
	s.awaitState(t, "credential-rotated", orders.StateBlocked)
	s.workersEnabled(t, false)
	if s.provider.writeCount("credential-rotated") != 0 {
		t.Fatal("frozen intent survived a target credential rotation")
	}
	config := s.a.config
	config.Automation = &orders.AutomationGrant{Actions: []orders.Action{orders.ActionMirrorCreate}, Fields: []string{"name", "state", "positions.quantity", "positions.price", "positions.vat"}, ExpiresAt: time.Now().Add(500 * time.Millisecond)}
	for _, scheduler := range s.schedules {
		scheduler.configure(t, false, "", true)
	}
	s.a.saveConfig(t, config)
	time.Sleep(600 * time.Millisecond)
	s.provider.add(s.a.scope.OzonAccount, "expired-grant", "FBS", false)
	s.a.check(t)
	for _, scheduler := range s.schedules {
		scheduler.configure(t, true, "", true)
	}
	time.Sleep(700 * time.Millisecond)
	var count int
	if err := s.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders_work WHERE work->'command'->>'posting_number'='expired-grant'`).Scan(&count); err != nil || count != 0 || s.provider.writeCount("expired-grant") != 0 {
		t.Fatal("expired owner grant created automatic work or a provider mutation")
	}
}
