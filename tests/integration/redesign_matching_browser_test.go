//go:build integration

package integration

import (
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/lilter96/styk/internal/console"
)

func TestRedesignMatchingBrowser(t *testing.T) {
	if os.Getenv("STYK_REDESIGN_MATCHING_E2E") != "1" {
		t.Skip("redesign matching browser test not requested")
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_redesign_test" {
		t.Fatal("dedicated local redesign test database required")
	}
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_CLIENT_ID", "OZON_API_KEY", "STYK_CREDENTIAL_ENCRYPTION_KEY"} {
		t.Setenv(key, "")
	}
	f := newStockFixture(t)
	seedRedesignCatalog(t, f)
	seedRedesignTargets(t, f)
	h, err := console.FixtureHandler(f.store, "", "http://127.0.0.1:15179")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(h)
	defer api.Close()
	t.Setenv("STYK_REDESIGN_MATCHING_SCOPE", redesignScope(f))
	runSettingsBrowser(t, api.URL, "e2e/redesign-matching.spec.ts")
}
