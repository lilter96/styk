package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestPermanentGrantChangeRequiresConsentOnlyForExpansion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	tests := []struct {
		name    string
		consent bool
		change  func(*orders.AutomationGrant)
		blocked bool
	}{
		{"unchanged", false, func(*orders.AutomationGrant) {}, false},
		{"reduced", false, func(g *orders.AutomationGrant) { g.Actions = g.Actions[:1]; g.Fields = g.Fields[:1] }, false},
		{"new field", false, func(g *orders.AutomationGrant) { g.Fields = append(g.Fields, "moment") }, true},
		{"explicit expansion", true, func(g *orders.AutomationGrant) { g.Fields = append(g.Fields, "moment") }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior := orders.AutomationGrant{ID: "grant", Scope: scope, Revision: 1, ConfigRevision: 1, OwnerID: "owner", SourceAccount: "ozon", GrantedAt: now, Perpetual: true, Actions: []orders.Action{orders.ActionMirrorCreate, orders.ActionMirrorUpdate}, Fields: []string{"name", "state"}}
			next := prior
			tc.change(&next)
			config := orders.Config{Scope: scope, Revision: 1, Automation: &prior}
			request := orders.SaveConfigRequest{Config: orders.Config{Scope: scope, Automation: &next}, AutomationConsent: tc.consent}
			err := validateOrderGrantChange(request, config, "owner", now)
			if errors.Is(err, orders.ErrBlocked) != tc.blocked {
				t.Fatalf("blocked=%v error=%v", tc.blocked, err)
			}
		})
	}
}
