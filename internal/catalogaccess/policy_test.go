package catalogaccess

import (
	"github.com/lilter96/styk/internal/catalog"
	"testing"
)

func TestAllValidConnectedScopesAvailable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		scope catalog.Scope
		want  bool
	}{
		{"first merchant", catalog.Scope{Tenant: "first", MoyskladAccount: "moy", OzonAccount: "900001"}, true},
		{"second merchant", catalog.Scope{Tenant: "second", MoyskladAccount: "other", OzonAccount: "900002"}, true},
		{"invalid", catalog.Scope{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if Allows(tc.scope) != tc.want {
				t.Fatal("unexpected scope availability")
			}
		})
	}
	if !Enabled() {
		t.Fatal("catalog worker unavailable")
	}
}
