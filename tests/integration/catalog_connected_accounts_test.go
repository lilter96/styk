//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func TestCatalogConnectedAccountsKeepDurableApprovalAndDispatchGuards(t *testing.T) {
	tests := []struct{ name, tenant, moy, ozon string }{
		{"first merchant", "catalog-merchant-one", "10000000-0000-4000-8000-000000000001", "900001"},
		{"second merchant", "catalog-merchant-two", "20000000-0000-4000-8000-000000000001", "900002"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, config := catalogSyncFixtureForScope(t, catalog.Scope{Tenant: tc.tenant, MoyskladAccount: tc.moy, OzonAccount: tc.ozon})
			preview := catalogSyncPreview(t, db, config)
			operation := catalogSyncApprove(t, db, preview)
			again := catalogSyncApprove(t, db, preview)
			if again.ID != operation.ID {
				t.Fatal("replayed approval created another operation")
			}
			work := catalogSyncClaim(t, db)
			guard := catalogsync.DispatchGuard{Work: work, Now: time.Now(), Begin: true}
			allowed, err := db.GuardCatalogDispatch(context.Background(), guard)
			if err != nil || !allowed {
				t.Fatalf("reviewed connected scope was blocked: %v", err)
			}
			allowed, err = db.GuardCatalogDispatch(context.Background(), guard)
			if err != nil || allowed {
				t.Fatalf("duplicate dispatch passed: allowed=%v err=%v", allowed, err)
			}
		})
	}
}

func TestCatalogConnectedAccountHTTPStillRequiresMerchantOwnership(t *testing.T) {
	h := newCatalogSecurityHTTP(t)
	scope := h.workflow.scope
	scope.OzonAccount = "900003"
	if err := h.workflow.db.RegisterStockConnection(t.Context(), scope.Tenant, "ozon", scope.OzonAccount, "synthetic connected catalog account", false); err != nil {
		t.Fatal(err)
	}
	config := catalogsync.Config{Scope: scope, Enabled: true, TestEnabled: true, IntervalSeconds: 300, Rules: catalog.Policies{}, Targets: []catalogsync.Enrollment{}}
	body := catalogSecurityScopeBody(scope)
	body["config"], body["expected_revision"], body["idempotency_key"] = config, 0, "connected-account-config"
	status, _ := h.owner.request(t, http.MethodPost, "/api/catalog-sync/config", catalogSecurityJSON(t, body), true)
	if status != http.StatusOK {
		t.Fatalf("owned non-test account was blocked: status=%d", status)
	}
	status, _ = h.other.request(t, http.MethodGet, "/api/catalog-sync/config"+catalogSecurityScopeQuery(scope), "", false)
	if status != http.StatusNotFound {
		t.Fatalf("foreign merchant could read the connected scope: status=%d", status)
	}
	if h.blocked.Load() != 0 {
		t.Fatal("configuration unexpectedly contacted a provider")
	}
}
