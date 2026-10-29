package orders

import (
	"slices"
	"testing"
	"time"
)

func TestAutomaticWriteChecksExplicitEmptyAndZero(t *testing.T) {
	t.Parallel()
	command := testCommand(t)
	command.AutomationGrantRevision = 1
	command.CustomerOrder.Description = Field[string]{Presence: PresenceEmpty}
	command.CustomerOrder.Positions[0].Reserve = Present(Decimal("0"))
	now := command.CreatedAt
	grant := AutomationGrant{ID: "grant-1", Scope: command.Scope, Revision: 1, ConfigRevision: 1, OwnerID: "stable-user-1",
		SourceAccount: command.Scope.OzonAccount, GrantedAt: now, ExpiresAt: now.Add(time.Hour),
		Actions: []Action{ActionMirrorCreate}, Fields: []string{"state", "positions.quantity"}}
	if !slices.Contains(command.Fields(), "description") || !slices.Contains(command.Fields(), "positions.reserve") {
		t.Fatal("explicit empty or zero was omitted from command authorization")
	}
	if grant.Allows(command, now) {
		t.Fatal("an ungranted empty or zero field was allowed")
	}
	grant.Fields = append(grant.Fields, "description", "positions.reserve")
	if !grant.Allows(command, now) {
		t.Fatal("explicit field grant was rejected")
	}
	grant.OwnerID = ""
	if grant.Allows(command, now) {
		t.Fatal("grant without stable user identity was allowed")
	}
}

func TestAutomaticWriteCannotWidenScopeOrLifetime(t *testing.T) {
	t.Parallel()
	command := testCommand(t)
	command.AutomationGrantRevision = 1
	now := command.CreatedAt
	grant := AutomationGrant{ID: "grant-1", Scope: command.Scope, Revision: 1, ConfigRevision: 1, OwnerID: "stable-user-1",
		SourceAccount: command.Scope.OzonAccount, GrantedAt: now, ExpiresAt: now.Add(time.Hour),
		Actions: []Action{ActionMirrorCreate}, Fields: command.Fields()}
	if !grant.Allows(command, now) || grant.Allows(command, grant.ExpiresAt) {
		t.Fatal("grant expiry was not respected")
	}
	grant.SourceAccount = "other-account"
	if grant.Allows(command, now) {
		t.Fatal("grant crossed its source account scope")
	}
	grant.SourceAccount = command.Scope.OzonAccount
	grant.ExpiresAt = now.Add(MaxAutomationGrantTTL + time.Second)
	if grant.Allows(command, now) {
		t.Fatal("grant exceeded its maximum lifetime")
	}
}
