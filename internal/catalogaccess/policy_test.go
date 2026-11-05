package catalogaccess

import (
	"testing"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestCatalogDeploymentSwitchAndLegacyCompatibility(t *testing.T) {
	legacy := testpublication.Scope()
	fixed := catalog.Scope{Tenant: legacy.Tenant, MoyskladAccount: legacy.MoyskladAccount, OzonAccount: legacy.OzonAccount}
	merchant := catalog.Scope{Tenant: "another-merchant", MoyskladAccount: "another-moy", OzonAccount: "900001"}
	tests := []struct {
		name, general, legacy string
		scope                 catalog.Scope
		want                  bool
	}{
		{"default off", "", "", merchant, false},
		{"all connected accounts", "true", "", merchant, true},
		{"fixed pair also supported", "true", "", fixed, true},
		{"legacy fixed pair", "", "true", fixed, true},
		{"legacy cannot widen", "", "true", merchant, false},
		{"invalid scope", "true", "true", catalog.Scope{}, false},
		{"explicit false", "false", "", merchant, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STYK_CATALOG_PUBLICATION_ENABLED", tc.general)
			t.Setenv("STYK_TEST_ACCOUNT_CATALOG", tc.legacy)
			t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
			if got := Allows(tc.scope); got != tc.want {
				t.Fatalf("Allows=%v want %v", got, tc.want)
			}
			if Enabled() != (tc.general == "true" || tc.legacy == "true") {
				t.Fatal("worker activation differs from deployment switches")
			}
		})
	}
}
