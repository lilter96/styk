package orders

import (
	"testing"
	"time"
)

func TestPerpetualGrantRetainsEveryAuthorityFence(t *testing.T) {
	t.Parallel()
	command := testCommand(t)
	command.AutomationGrantRevision = 1
	now := command.CreatedAt
	base := AutomationGrant{ID: "permanent", Scope: command.Scope, Revision: 1, ConfigRevision: command.ConfigRevision, OwnerID: "owner", SourceAccount: command.Scope.OzonAccount, GrantedAt: now, Perpetual: true, Actions: []Action{ActionMirrorCreate}, Fields: command.Fields()}
	tests := []struct {
		name    string
		mutate  func(*AutomationGrant)
		allowed bool
	}{
		{"after two days", func(*AutomationGrant) {}, true},
		{"revoked", func(g *AutomationGrant) { g.RevokedAt = &now }, false},
		{"another account", func(g *AutomationGrant) { g.Scope.OzonAccount = "another" }, false},
		{"stale revision", func(g *AutomationGrant) { g.ConfigRevision++ }, false},
		{"field removed", func(g *AutomationGrant) { g.Fields = nil }, false},
		{"action removed", func(g *AutomationGrant) { g.Actions = []Action{ActionMirrorUpdate} }, false},
		{"expiry mixed with permanent", func(g *AutomationGrant) { g.ExpiresAt = now.Add(time.Hour) }, false},
		{"Ozon action", func(g *AutomationGrant) { g.Actions = []Action{ActionPostingShip} }, false},
		{"native action", func(g *AutomationGrant) { g.Actions = []Action{ActionDemandCreate} }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			grant := base
			tc.mutate(&grant)
			if got := grant.Allows(command, now.Add(48*time.Hour)); got != tc.allowed {
				t.Fatalf("Allows=%v want %v", got, tc.allowed)
			}
		})
	}
}

func TestLegacyGrantDoesNotBecomePermanent(t *testing.T) {
	t.Parallel()
	command := testCommand(t)
	command.AutomationGrantRevision = 1
	now := command.CreatedAt
	grant := AutomationGrant{ID: "legacy", Scope: command.Scope, Revision: 1, ConfigRevision: command.ConfigRevision, OwnerID: "owner", SourceAccount: command.Scope.OzonAccount, GrantedAt: now, ExpiresAt: now.Add(time.Hour), Actions: []Action{ActionMirrorCreate}, Fields: command.Fields()}
	if !grant.Allows(command, now) || grant.Allows(command, now.Add(48*time.Hour)) {
		t.Fatal("legacy expiry was changed")
	}
}

func TestPermanentUpdateIncludesOnlyOwnedPositionPatches(t *testing.T) {
	t.Parallel()
	command, baseline := positionPatchFixture(t)
	command.CustomerOrder.Positions[0].Quantity = "3"
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil || patch == nil {
		t.Fatal("prepare owned position patch", err)
	}
	command.Action, command.PositionPatch = ActionMirrorPositionUpdate, patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	command.AutomationGrantRevision = 1
	now := command.CreatedAt
	grant := AutomationGrant{ID: "permanent-update", Scope: command.Scope, Revision: 1, ConfigRevision: command.ConfigRevision, OwnerID: "owner", SourceAccount: command.Scope.OzonAccount, GrantedAt: now, Perpetual: true, Actions: []Action{ActionMirrorUpdate}, Fields: command.Fields()}
	if !grant.Allows(command, now.Add(48*time.Hour)) {
		t.Fatal("permanent update excluded a proven position patch")
	}
	grant.Perpetual = false
	grant.ExpiresAt = now.Add(time.Hour)
	if grant.Allows(command, now) {
		t.Fatal("legacy exact action authority was widened")
	}
}
