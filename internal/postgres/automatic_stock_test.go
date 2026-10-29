package postgres

import (
	"testing"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestAutomaticScopesRequireFixedRealPolicyOrLoopbackSimulation(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "true")
	scope := domain.StockSyncScope{Tenant: "synthetic-auto-one", MoyskladAccount: "synthetic-source", OzonAccount: "synthetic-market"}
	for _, endpoint := range []string{"", "https://api-seller.ozon.ru", "http://localhost:1234", "http://127.0.0.1.evil.example:1234"} {
		t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", endpoint)
		if automaticScopeAllowed(scope) {
			t.Fatalf("unsafe simulator endpoint accepted: %s", endpoint)
		}
	}
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "http://127.0.0.1:1234")
	if !automaticScopeAllowed(scope) {
		t.Fatal("explicit loopback simulation unavailable")
	}
	ordinary := scope
	ordinary.Tenant = "ordinary-customer"
	if automaticScopeAllowed(ordinary) {
		t.Fatal("simulator flag authorized an ordinary tenant")
	}
	t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", "")
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	if !automaticScopeAllowed(testpublication.Scope()) {
		t.Fatal("fixed owner-controlled policy unavailable")
	}
	other := testpublication.Scope()
	other.OzonAccount = "other"
	if automaticScopeAllowed(other) {
		t.Fatal("test switch widened real account scope")
	}
}

func TestAutomaticReadFailureKeepsStrictestProviderRestriction(t *testing.T) {
	for _, tc := range []struct {
		a, b  int
		rates int
		want  string
		retry bool
	}{
		{403, 500, 0, "AUTHORIZATION_REQUIRED", false}, {500, 429, 0, "RATE_LIMITED", false}, {500, 0, 1, "RATE_LIMITED", false}, {500, 0, 0, "RETRYABLE_READ_FAILURE", true},
	} {
		a := domain.ImportOperation{State: "FAILED", Counts: domain.ImportCounts{HTTPStatus: tc.a, RateLimitResponses: tc.rates}}
		b := domain.ImportOperation{State: "FAILED", Counts: domain.ImportCounts{HTTPStatus: tc.b}}
		got, retry := automaticReadFailure(a, b)
		if got != tc.want || retry != tc.retry {
			t.Fatalf("statuses %d,%d: %s retry=%v", tc.a, tc.b, got, retry)
		}
	}
}
