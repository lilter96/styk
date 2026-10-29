package postgres

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestMetadataGuardRequiresFreshExactOperationalIntent(t *testing.T) {
	t.Parallel()
	base := orders.MetadataCreationIntent{ID: "0123456789abcdef0123456789abcdef", Scope: orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, UniqueCode: "styk-order-status", Type: "string", State: orders.StateDispatching, DispatchPermitted: true}
	base.Marker = "styk-order-attribute-" + base.ID
	for _, test := range []struct {
		name   string
		mutate func(*orders.MetadataCreationIntent)
		valid  bool
	}{{"fresh", func(*orders.MetadataCreationIntent) {}, true}, {"replayed", func(intent *orders.MetadataCreationIntent) { intent.DispatchPermitted = false }, false}, {"unknown", func(intent *orders.MetadataCreationIntent) { intent.State = orders.StateUnknown }, false}, {"pii", func(intent *orders.MetadataCreationIntent) { intent.UniqueCode = "customer.phone" }, false}, {"different marker", func(intent *orders.MetadataCreationIntent) { intent.Marker += "other" }, false}, {"unsupported type", func(intent *orders.MetadataCreationIntent) { intent.Type = "long" }, false}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			intent := base
			test.mutate(&intent)
			if validOrderMetadataGuard(intent, "actor", 2) != test.valid {
				t.Fatal("metadata guard validity mismatch")
			}
		})
	}
}

func TestMetadataGuardComparesInstantAndEveryScopedIdentity(t *testing.T) {
	t.Parallel()
	intent := orders.MetadataCreationIntent{ID: "intent", Scope: orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, CreatedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.FixedZone("Moscow", 3*60*60)), DispatchPermitted: true}
	stored := intent
	stored.CreatedAt, stored.DispatchPermitted = intent.CreatedAt.UTC(), false
	if !sameOrderMetadataIntent(intent, stored) {
		t.Fatal("JSON timestamp timezone changed durable intent equality")
	}
	stored.Scope.OzonAccount = "another-ozon-account"
	if sameOrderMetadataIntent(intent, stored) {
		t.Fatal("metadata guard accepted another scoped account tuple")
	}
}
