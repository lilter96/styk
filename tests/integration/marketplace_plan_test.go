//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

const planOrigin = "http://127.0.0.1:15179"

type planSession struct {
	token string
	identity.Session
}

func planMerchant(t *testing.T, db *postgres.Store, email string) (string, planSession) {
	t.Helper()
	store, err := identity.New(db.Pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	user, workspace, err := store.Register(t.Context(), email, "synthetic-password", email)
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, user.Email)
	return workspace.ID, planLogin(t, store, email)
}

func planLogin(t *testing.T, store *identity.Store, email string) planSession {
	t.Helper()
	user, err := store.Authenticate(t.Context(), email, "long-password-owner")
	if err != nil {
		t.Fatal(err)
	}
	session, token, err := store.CreateSession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	return planSession{token, session}
}

func planTestHandler(t *testing.T, db *postgres.Store, adminID string) http.Handler {
	t.Helper()
	t.Setenv("STYK_PLATFORM_ADMIN_IDS", adminID)
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{13}, 32)))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/product/list" || r.Method != "POST" {
			t.Error("unexpected synthetic provider request")
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		merchantFakeReply(w, `{"result":{"items":[]}}`)
	}))
	t.Cleanup(provider.Close)
	handler, err := console.MerchantLoopbackTestHandler(db, planOrigin, &testMailbox{tokens: map[string]string{}}, provider.URL, provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func planRequest(handler http.Handler, session planSession, method, path string, body any, support bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, planOrigin+path, bytes.NewReader(raw))
	req.RemoteAddr = "127.0.0.1:42500"
	req.Header.Set("Origin", planOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-STYK-CSRF", session.CSRF)
	req.AddCookie(&http.Cookie{Name: "styk_session", Value: session.token})
	if support {
		req.AddCookie(&http.Cookie{Name: "styk_support", Value: "fixture"})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func assertPlanResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || code != "" && !strings.Contains(response.Body.String(), `"error":"`+code+`"`) {
		t.Fatalf("response %d %s, want %d %s", response.Code, response.Body.String(), status, code)
	}
}

func planKey(account, requestID string, revision int) map[string]any {
	return map[string]any{"account": account, "name": "Ozon synthetic", "secret": "synthetic-api-key", "expected_revision": revision, "request_id": requestID, "acknowledge": true}
}

func TestMarketplacePlanConcurrentAdmission(t *testing.T) {
	db := catalogSyncDB(t)
	tenant, first := planMerchant(t, db, "quota-race@example.test")
	store, err := identity.New(db.Pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second := planLogin(t, store, first.User.Email)
	handler := planTestHandler(t, db, "")
	const path = "/api/integrations/ozon/credentials"
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	bodies := []map[string]any{planKey("72501", "quota-first", 0), planKey("72502", "quota-second", 0)}
	for i, session := range []planSession{first, second} {
		go func() { <-start; results <- planRequest(handler, session, "POST", path, bodies[i], false) }()
	}
	close(start)
	success, blocked := 0, 0
	for range 2 {
		response := <-results
		if response.Code == 200 {
			success++
		} else {
			assertPlanResponse(t, response, 409, "PLAN_TOTAL_LIMIT")
			blocked++
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatalf("success=%d, blocked=%d", success, blocked)
	}
	assertPlanCounts(t, db, tenant, 1)
	if err := db.RegisterStockConnection(t.Context(), tenant, "ozon", "72503", "synthetic direct registration", false); !errors.Is(err, domain.ErrPlanTotalLimit) {
		t.Fatalf("operator quota bypass: %v", err)
	}
	state, err := db.MarketplacePlan(t.Context(), tenant)
	if err != nil || state.Code != "plan_1" || state.Usage.Total() != 1 {
		t.Fatalf("default plan snapshot = %+v: %v", state, err)
	}
}

func assertPlanCounts(t *testing.T, db *postgres.Store, tenant string, want int) {
	t.Helper()
	var registrations, keys, ledger int
	err := db.Pool.QueryRow(t.Context(), `SELECT
	 (SELECT count(*) FROM stock_connections WHERE tenant_id=$1 AND source='ozon'),
	 (SELECT count(*) FROM connection_credentials WHERE tenant_id=$1 AND provider='ozon'),
	 (SELECT count(*) FROM credential_save_requests WHERE tenant_id=$1 AND provider='ozon')`, tenant).Scan(&registrations, &keys, &ledger)
	if err != nil || registrations != want || keys != want || ledger != want {
		t.Fatalf("partially saved quota write: %d %d %d: %v", registrations, keys, ledger, err)
	}
}

func TestMarketplacePlanAdministrationAndLifecycle(t *testing.T) {
	db := catalogSyncDB(t)
	_, admin := planMerchant(t, db, "quota-admin@example.test")
	tenant, owner := planMerchant(t, db, "quota-owner@example.test")
	handler := planTestHandler(t, db, admin.User.ID)
	planPath := "/api/admin/plans/" + tenant
	setPlan := func(session planSession, code string, revision int, support bool) *httptest.ResponseRecorder {
		return planRequest(handler, session, "POST", planPath, map[string]any{"code": code, "expected_revision": revision}, support)
	}
	assertPlanResponse(t, setPlan(owner, "plan_3", 1, false), 403, "FORBIDDEN")
	assertPlanResponse(t, setPlan(admin, "plan_3", 1, true), 409, "SUPPORT_ACTIVE")
	assertPlanResponse(t, planRequest(handler, owner, "GET", "/api/marketplace-plan?tenant=foreign", nil, false), 404, "NOT_FOUND")
	assertPlanResponse(t, setPlan(admin, "plan_3", 1, false), 200, "")
	assertPlanResponse(t, setPlan(admin, "plan_4", 1, false), 409, "PLAN_REVISION_CONFLICT")
	const keyPath = "/api/integrations/ozon/credentials"
	first := planKey("72511", "first-plan-key", 0)
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, first, false), 200, "")
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, planKey("72512", "second-plan-key", 0), false), 200, "")
	assertPlanResponse(t, setPlan(admin, "plan_2", 2, false), 200, "")
	assertPlanResponse(t, setPlan(admin, "plan_2", 3, false), 200, "")
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, first, false), 200, "")
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, planKey("72511", "rotate-plan-key", 1), false), 200, "")
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, planKey("72513", "exceeds-plan", 0), false), 409, "PLAN_OVER_LIMIT")
	disconnect := map[string]any{"account": "72512", "expected_revision": 1, "request_id": "disconnect-plan-key", "acknowledge": true}
	assertPlanResponse(t, planRequest(handler, owner, "POST", "/api/integrations/ozon/disconnect", disconnect, false), 200, "")
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, planKey("72512", "reactivate-plan-key", 2), false), 409, "PLAN_PROVIDER_LIMIT")
	assertPlanResponse(t, planRequest(handler, admin, "GET", "/api/admin/plans?q=quota-owner", nil, false), 200, "")
	state, err := db.MarketplacePlan(t.Context(), tenant)
	if err != nil || state.Code != "plan_2" || state.Revision != 3 || state.Usage.Total() != 1 {
		t.Fatalf("unexpected plan state %+v: %v", state, err)
	}
	var audits int
	err = db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM saas_security_events WHERE workspace_id=$1 AND actor_user_id=$2 AND action='MARKETPLACE_PLAN_CHANGED'`, tenant, admin.User.ID).Scan(&audits)
	if err != nil || audits != 2 {
		t.Fatalf("unexpected tariff audit count %d: %v", audits, err)
	}
}

func TestMarketplacePlanDisconnectsLegacyFallback(t *testing.T) {
	db := catalogSyncDB(t)
	tenant, owner := planMerchant(t, db, "quota-legacy@example.test")
	const account = "72521"
	handler := planTestHandler(t, db, "")
	t.Setenv("OZON_CLIENT_ID", account)
	t.Setenv("OZON_API_KEY", "synthetic-fallback")
	ctx := context.Background()
	if err := db.RegisterStockConnection(ctx, tenant, "ozon", account, "legacy fixture", false); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterStockConnection(ctx, tenant, "moysklad", "legacy-moy", "legacy fixture", false); err != nil {
		t.Fatal(err)
	}
	vault, err := credentials.FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	resolver := credentials.Resolver{Store: db, Vault: vault}
	scope := credentials.Scope{Tenant: tenant, Provider: "ozon", Account: account}
	if _, revision, err := resolver.ResolveWithRevision(ctx, scope); err != nil || revision != 0 {
		t.Fatalf("expected legacy fallback: %d %v", revision, err)
	}
	body := map[string]any{"account": account, "expected_revision": 0, "request_id": "legacy-disconnect", "acknowledge": true}
	assertPlanResponse(t, planRequest(handler, owner, "POST", "/api/integrations/ozon/disconnect", body, false), 200, "")
	if _, _, err := resolver.ResolveWithRevision(ctx, scope); !errors.Is(err, credentials.ErrUnavailable) {
		t.Fatalf("legacy fallback still resolved: %v", err)
	}
	if owned, err := db.CredentialConnectionOwned(ctx, scope); err != nil || !owned {
		t.Fatalf("legacy account ownership was removed: %v", err)
	}
	assertPlanResponse(t, planRequest(handler, owner, "POST", "/api/integrations/ozon/credentials", planKey("72522", "replace-disconnected", 0), false), 200, "")
}

func TestMarketplacePlanMigrationBackfillsExistingCompanies(t *testing.T) {
	db := catalogSyncDB(t)
	dsn := db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down")
	ctx := t.Context()
	for _, fixture := range []struct {
		name     string
		cabinets int
		want     string
	}{
		{"initial", 0, "plan_1"}, {"single", 1, "plan_1"}, {"two-same", 2, "plan_3"}, {"six", 6, "plan_4"}, {"over", 21, "plan_4"},
	} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO saas_workspaces(id,name) VALUES($1,$1)`, fixture.name); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_connections(tenant_id,source,account_id,provenance)
		 SELECT $1,'ozon',$1||'-'||n,'synthetic' FROM generate_series(1,$2::int) n`, fixture.name, fixture.cabinets); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			var got string
			if err := db.Pool.QueryRow(context.Background(), `SELECT marketplace_plan FROM saas_workspaces WHERE id=$1`, fixture.name).Scan(&got); err != nil || got != fixture.want {
				t.Errorf("%s backfill = %s, want %s: %v", fixture.name, got, fixture.want, err)
			}
		})
	}
	catalogSyncMigrate(t, dsn, "up")
	if err := db.RequireCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMarketplacePlanSerializesDowngradeAndRollsBackAuditFailure(t *testing.T) {
	db := catalogSyncDB(t)
	_, admin := planMerchant(t, db, "quota-serial-admin@example.test")
	tenant, owner := planMerchant(t, db, "quota-serial-owner@example.test")
	handler := planTestHandler(t, db, admin.User.ID)
	const keyPath = "/api/integrations/ozon/credentials"
	planPath := "/api/admin/plans/" + tenant
	assertPlanResponse(t, planRequest(handler, admin, "POST", planPath, map[string]any{"code": "plan_3", "expected_revision": 1}, false), 200, "")
	assertPlanResponse(t, planRequest(handler, owner, "POST", keyPath, planKey("72531", "serial-first", 0), false), 200, "")
	adding := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		adding <- planRequest(handler, owner, "POST", keyPath, planKey("72532", "serial-second", 0), false)
	}()
	downgrade := planRequest(handler, admin, "POST", planPath, map[string]any{"code": "plan_1", "expected_revision": 2}, false)
	added := <-adding
	assertPlanResponse(t, downgrade, 200, "")
	wantUsed := 2
	if added.Code != 200 {
		assertPlanResponse(t, added, 409, "PLAN_TOTAL_LIMIT")
		wantUsed = 1
	}
	assertPlanCounts(t, db, tenant, wantUsed)
	if _, err := db.Pool.Exec(t.Context(), `ALTER TABLE saas_security_events ADD CONSTRAINT fixture_plan_audit_failure CHECK (action<>'MARKETPLACE_PLAN_CHANGED') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	assertPlanResponse(t, planRequest(handler, admin, "POST", planPath, map[string]any{"code": "plan_4", "expected_revision": 3}, false), 503, "DATABASE_UNAVAILABLE")
	state, err := db.MarketplacePlan(t.Context(), tenant)
	if err != nil || state.Code != "plan_1" || state.Revision != 3 || state.Usage.Total() != wantUsed {
		t.Fatalf("plan change survived an audit rollback: %+v, %v", state, err)
	}
}
