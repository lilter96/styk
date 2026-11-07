package postgres

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func lifecycleConfigFixture() (orders.Config, time.Time) {
	now := time.Now().UTC()
	scope := orders.Scope{Tenant: "merchant", MoyskladAccount: "native", OzonAccount: "market"}
	return orders.Config{Scope: scope, Revision: 3, Enabled: true, TestEnabled: true, Lifecycle: &orders.LifecyclePolicy{ID: "00000000-0000-4000-8000-000000000011", Scope: scope, Revision: 3, ConfigRevision: 3, OwnerID: "owner", Enabled: true, GrantedAt: now.Add(-time.Hour), DemandMoment: orders.OnHandoffConfirmed, Mappings: []orders.MerchantCommandMapping{{StateID: "00000000-0000-4000-8000-000000000012", FromStateIDs: []string{"00000000-0000-4000-8000-000000000013"}, Scheme: "FBS", Action: orders.ActionPostingPack, EventKind: "assembly_ready", WholePosting: true}}}}, now
}

func TestLifecycleRequiresIndependentOwnerConsent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.SaveConfigRequest, *orders.Config)
		allow  bool
	}{
		{name: "unchanged policy retained", allow: true},
		{name: "new policy without consent", change: func(_ *orders.SaveConfigRequest, p *orders.Config) { p.Lifecycle = nil }},
		{name: "mirror consent grants no new authority", change: func(r *orders.SaveConfigRequest, p *orders.Config) { p.Lifecycle = nil; r.AutomationConsent = true }},
		{name: "explicit lifecycle consent", allow: true, change: func(r *orders.SaveConfigRequest, p *orders.Config) { p.Lifecycle = nil; r.LifecycleConsent = true }},
		{name: "expanded native rights", change: func(r *orders.SaveConfigRequest, _ *orders.Config) { r.Config.Lifecycle.AutoReturn = true }},
		{name: "changed mapping", change: func(r *orders.SaveConfigRequest, _ *orders.Config) {
			r.Config.Lifecycle.Mappings[0].StateID = "00000000-0000-4000-8000-000000000014"
		}},
		{name: "revoke without additional permission", allow: true, change: func(r *orders.SaveConfigRequest, _ *orders.Config) { r.Config.Lifecycle.Enabled = false }},
		{name: "disabled reads cannot silently run policy", change: func(r *orders.SaveConfigRequest, _ *orders.Config) { enabled := false; r.ReadEnabled = &enabled }},
		{name: "previous read revocation persists", change: func(_ *orders.SaveConfigRequest, p *orders.Config) { p.ReadConfigured = true; p.ReadEnabled = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior, now := lifecycleConfigFixture()
			next, _ := lifecycleConfigFixture()
			request := orders.SaveConfigRequest{Config: next}
			if tc.change != nil {
				tc.change(&request, &prior)
			}
			err := validateOrderLifecycleChange(request, prior, "owner", now)
			if (err == nil) != tc.allow {
				t.Fatalf("consent admitted=%v want=%v err=%v", err == nil, tc.allow, err)
			}
		})
	}
}

func TestLifecycleFinalizerAssignsCanonicalServerAuthority(t *testing.T) {
	t.Parallel()
	config, now := lifecycleConfigFixture()
	prior := config
	prior.Lifecycle = nil
	config.Lifecycle.ID = "untrusted"
	config.Lifecycle.OwnerID = "other"
	if err := finalizeOrderLifecycle(&config, prior, "owner", now); err != nil {
		t.Fatal(err)
	}
	if err := config.Lifecycle.Validate(); err != nil {
		t.Fatal("server policy is not valid", err)
	}
	if config.Lifecycle.OwnerID != "owner" || config.Lifecycle.ConfigRevision != config.Revision || config.Lifecycle.Scope != config.Scope {
		t.Fatal("authority was accepted from caller")
	}
}
