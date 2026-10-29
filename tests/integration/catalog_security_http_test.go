//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/identity"
)

var errCatalogSecurityNetwork = errors.New("catalog security test forbids outbound connections")

type catalogSecurityHTTP struct {
	workflow    *catalogSecurityWorkflow
	operation   catalogsync.Operation
	owner       *merchantBrowser
	other       *merchantBrowser
	otherTenant string
	ownerAudit  string
	blocked     *atomic.Int64
}

func newCatalogSecurityHTTP(t *testing.T) *catalogSecurityHTTP {
	t.Helper()
	f := newCatalogSecurityWorkflow(t)
	h := &catalogSecurityHTTP{workflow: f, operation: f.approve(t)}
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	t.Setenv("STYK_TEST_ACCOUNT_CATALOG", "true")
	store, err := identity.New(f.db.Pool, f.db.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	owner := catalogSecurityHTTPUser(t, store, "catalog-owner@example.test")
	other := catalogSecurityHTTPUser(t, store, "catalog-other@example.test")
	if err = store.ClaimLegacy(t.Context(), f.scope.Tenant, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.BindLegacyMerchant(t.Context(), owner.ID, f.scope.Tenant); err != nil {
		t.Fatal(err)
	}
	h.otherTenant, err = store.MerchantTenant(t.Context(), other.ID)
	if err != nil || h.otherTenant == f.scope.Tenant {
		t.Fatalf("distinct merchant account required: %v", err)
	}
	handler, err := console.MerchantHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: make(map[string]string)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	h.blocked = catalogSecurityHTTPNetwork(t, server.URL)
	h.owner, h.ownerAudit = catalogSecurityHTTPBrowser(t, store, owner, server.URL)
	h.other, _ = catalogSecurityHTTPBrowser(t, store, other, server.URL)
	return h
}

func catalogSecurityHTTPUser(t *testing.T, store *identity.Store, email string) identity.User {
	t.Helper()
	if _, _, err := store.Register(t.Context(), email, "long-password-owner", "Synthetic catalog security"); err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, email)
	user, err := store.Authenticate(t.Context(), email, "long-password-owner")
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func catalogSecurityHTTPBrowser(t *testing.T, store *identity.Store, user identity.User, base string) (*merchantBrowser, string) {
	t.Helper()
	session, token, err := store.CreateSession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	browser := newMerchantBrowser(t, base)
	browser.client.Jar.SetCookies(u, []*http.Cookie{{Name: "styk_session", Value: token, Path: "/"}})
	browser.csrf = session.CSRF
	return browser, session.AuditID
}

func catalogSecurityHTTPNetwork(t *testing.T, base string) *atomic.Int64 {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	standard, ok := original.(*http.Transport)
	if !ok {
		t.Fatal("standard HTTP transport required")
	}
	blocked := &atomic.Int64{}
	transport := standard.Clone()
	transport.Proxy, transport.DisableKeepAlives = nil, true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != u.Host {
			blocked.Add(1)
			return nil, errCatalogSecurityNetwork
		}
		dialer := net.Dialer{Timeout: time.Second}
		return dialer.DialContext(ctx, network, address)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		http.DefaultTransport = original
	})
	return blocked
}

func catalogSecurityJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func catalogSecurityScopeBody(scope catalog.Scope) map[string]any {
	return map[string]any{"tenant": scope.Tenant, "moysklad_account": scope.MoyskladAccount, "ozon_account": scope.OzonAccount}
}

func catalogSecurityScopeQuery(scope catalog.Scope) string {
	return "?" + url.Values{"tenant": {scope.Tenant}, "moysklad_account": {scope.MoyskladAccount}, "ozon_account": {scope.OzonAccount}}.Encode()
}

func (h *catalogSecurityHTTP) assertOwnerReads(t *testing.T) {
	t.Helper()
	query := catalogSecurityScopeQuery(h.workflow.scope)
	status, raw := h.owner.request(t, http.MethodGet, "/api/catalog-sync/config"+query, "", false)
	var config catalogsync.Config
	if status != http.StatusOK || json.Unmarshal(raw, &config) != nil || config.Scope != h.workflow.scope || config.Revision != h.workflow.config.Revision {
		t.Fatalf("owner catalog config control failed: status=%d", status)
	}
	status, raw = h.owner.request(t, http.MethodGet, "/api/catalog-sync/operations"+query, "", false)
	var history catalogsync.HistoryPage
	if status != http.StatusOK || json.Unmarshal(raw, &history) != nil || len(history.Operations) != 1 || history.Operations[0].ID != h.operation.ID {
		t.Fatalf("owner catalog history control failed: status=%d", status)
	}
}

func (h *catalogSecurityHTTP) assertUnchanged(t *testing.T) {
	t.Helper()
	var revisions, previews, operations, tasks int
	err := h.workflow.db.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM catalog_sync_config_revisions),(SELECT count(*) FROM catalog_sync_previews),(SELECT count(*) FROM catalog_sync_operations),(SELECT count(*) FROM outbox_tasks WHERE task_type='catalog_sync')`).Scan(&revisions, &previews, &operations, &tasks)
	if err != nil || revisions != 1 || previews != 1 || operations != 1 || tasks != 1 {
		t.Fatalf("rejected HTTP attack changed durable state: revisions=%d previews=%d operations=%d tasks=%d error=%v", revisions, previews, operations, tasks, err)
	}
	if h.workflow.target.writes != 0 || h.blocked.Load() != 0 {
		t.Fatalf("rejected HTTP attack reached provider dispatch: writes=%d blocked_connections=%d", h.workflow.target.writes, h.blocked.Load())
	}
}

type catalogSecurityHTTPRequest struct {
	name, method, path, body string
}

func catalogSecurityForeignHTTPRequests(t *testing.T, h *catalogSecurityHTTP, scope catalog.Scope) []catalogSecurityHTTPRequest {
	t.Helper()
	config := h.workflow.config
	config.Scope = scope
	configBody := catalogSecurityScopeBody(scope)
	configBody["config"], configBody["expected_revision"], configBody["idempotency_key"] = config, config.Revision, "foreign-config"
	preview := catalogSecurityScopeBody(scope)
	preview["expected_revision"], preview["enrollment_ids"] = config.Revision, []string{config.Targets[0].ID}
	approval := catalogSecurityScopeBody(scope)
	approval["preview_id"], approval["idempotency_key"], approval["acknowledge"] = h.operation.PreviewID, "foreign-approval", true
	query := catalogSecurityScopeQuery(scope)
	return []catalogSecurityHTTPRequest{
		{name: "config_read", method: http.MethodGet, path: "/api/catalog-sync/config" + query},
		{name: "products", method: http.MethodGet, path: "/api/catalog-sync/products" + query},
		{name: "results", method: http.MethodGet, path: "/api/catalog-sync/operations" + query},
		{name: "config_write", method: http.MethodPost, path: "/api/catalog-sync/config", body: catalogSecurityJSON(t, configBody)},
		{name: "preview", method: http.MethodPost, path: "/api/catalog-sync/preview", body: catalogSecurityJSON(t, preview)},
		{name: "approval", method: http.MethodPost, path: "/api/catalog-sync/approve", body: catalogSecurityJSON(t, approval)},
	}
}

func TestCatalogSecurityHTTPForeignTenantCannotReadOrApprove(t *testing.T) {
	h := newCatalogSecurityHTTP(t)
	h.assertOwnerReads(t)
	other := h.workflow.scope
	other.Tenant = h.otherTenant
	tests := []struct {
		name    string
		browser *merchantBrowser
		scope   catalog.Scope
	}{{name: "other_attacks_owner", browser: h.other, scope: h.workflow.scope}, {name: "owner_attacks_other", browser: h.owner, scope: other}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, request := range catalogSecurityForeignHTTPRequests(t, h, tc.scope) {
				t.Run(request.name, func(t *testing.T) {
					status, raw := tc.browser.request(t, request.method, request.path, request.body, true)
					if status != http.StatusNotFound || strings.Contains(string(raw), h.operation.ID) || strings.Contains(string(raw), h.operation.PreviewID) {
						t.Fatalf("foreign catalog object was not hidden: status=%d", status)
					}
				})
			}
		})
	}
	h.assertUnchanged(t)
}

func TestCatalogSecurityHTTPNestedScopeCannotOverrideMerchant(t *testing.T) {
	h := newCatalogSecurityHTTP(t)
	h.assertOwnerReads(t)
	foreign := h.workflow.scope
	foreign.Tenant = h.otherTenant
	tests := []struct {
		name    string
		browser *merchantBrowser
		top     catalog.Scope
		nested  catalog.Scope
	}{{name: "foreign_nested_scope", browser: h.owner, top: h.workflow.scope, nested: foreign}, {name: "owner_nested_scope", browser: h.other, top: foreign, nested: h.workflow.scope}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := h.workflow.config
			config.Scope = tc.nested
			body := catalogSecurityScopeBody(tc.top)
			body["config"], body["expected_revision"], body["idempotency_key"] = config, config.Revision, tc.name
			status, _ := tc.browser.request(t, http.MethodPost, "/api/catalog-sync/config", catalogSecurityJSON(t, body), true)
			if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
				t.Fatalf("nested config scope escaped the authenticated account: status=%d", status)
			}
		})
	}
	h.assertUnchanged(t)
}

func TestCatalogSecurityHTTPRejectsUnreviewedIdentitySubstitution(t *testing.T) {
	h := newCatalogSecurityHTTP(t)
	h.assertOwnerReads(t)
	tests := []struct {
		name   string
		change func(*catalogsync.Enrollment)
		status int
	}{
		{name: "source_entity", change: func(e *catalogsync.Enrollment) { e.Source.ID = "10000000-0000-0000-0000-000000000009" }, status: http.StatusConflict},
		{name: "destination_entity", change: func(e *catalogsync.Enrollment) { e.Target.ID = "999" }, status: http.StatusConflict},
		{name: "destination_offer", change: func(e *catalogsync.Enrollment) { e.Target.OfferID = "FOREIGN-OFFER" }, status: http.StatusConflict},
		{name: "source_account", change: func(e *catalogsync.Enrollment) { e.Source.Account.ID = "foreign-source" }, status: http.StatusBadRequest},
		{name: "destination_account", change: func(e *catalogsync.Enrollment) { e.Target.Account.ID = "foreign-destination" }, status: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := h.workflow.config
			enrollment := config.Targets[0]
			tc.change(&enrollment)
			config.Targets = []catalogsync.Enrollment{enrollment}
			body := catalogSecurityScopeBody(config.Scope)
			body["config"], body["expected_revision"], body["idempotency_key"] = config, config.Revision, tc.name
			status, _ := h.owner.request(t, http.MethodPost, "/api/catalog-sync/config", catalogSecurityJSON(t, body), true)
			if status != tc.status {
				t.Fatalf("unreviewed identity substitution returned status=%d want=%d", status, tc.status)
			}
		})
	}
	h.assertUnchanged(t)
}

func TestCatalogSecurityHTTPConfigRequiresCSRFAndAuthenticatedActor(t *testing.T) {
	h := newCatalogSecurityHTTP(t)
	h.assertOwnerReads(t)
	config := h.workflow.config
	config.IntervalSeconds++
	body := catalogSecurityScopeBody(h.workflow.scope)
	body["config"], body["expected_revision"], body["idempotency_key"] = config, config.Revision, "owner-config"
	encoded := catalogSecurityJSON(t, body)
	if status, _ := h.owner.request(t, http.MethodPost, "/api/catalog-sync/config", encoded, false); status != http.StatusForbidden {
		t.Fatalf("missing CSRF accepted: status=%d", status)
	}
	anonymous := newMerchantBrowser(t, h.owner.base)
	if status, _ := anonymous.request(t, http.MethodPost, "/api/catalog-sync/config", encoded, false); status != http.StatusUnauthorized {
		t.Fatalf("anonymous config mutation accepted: status=%d", status)
	}
	h.assertUnchanged(t)
	status, raw := h.owner.request(t, http.MethodPost, "/api/catalog-sync/config", encoded, true)
	var saved catalogsync.Config
	if status != http.StatusOK || json.Unmarshal(raw, &saved) != nil || saved.Revision != config.Revision+1 || saved.Scope != config.Scope {
		t.Fatalf("authenticated owner save control failed: status=%d", status)
	}
	var actor string
	err := h.workflow.db.Pool.QueryRow(t.Context(), `SELECT actor FROM catalog_sync_config_revisions WHERE revision=$1`, saved.Revision).Scan(&actor)
	if err != nil || actor != h.ownerAudit || h.blocked.Load() != 0 {
		t.Fatalf("save lost authenticated provenance or contacted provider: matched=%v blocked_connections=%d error=%v", actor == h.ownerAudit, h.blocked.Load(), err)
	}
}
