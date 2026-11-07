package orders

import (
	"testing"
	"time"
)

func nativeAutomaticDomainFixture(t *testing.T) (Command, Config, time.Time) {
	t.Helper()
	command, nativePolicy := nativeDomainFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	before := "00000000-0000-4000-8000-000000000021"
	after := "00000000-0000-4000-8000-000000000022"
	intent := MerchantIntent{ID: "00000000-0000-4000-8000-000000000023", Scope: command.Scope, Correlation: command.Correlation, BeforeStateID: before, AfterStateID: after, BeforeRevision: "before", AfterRevision: "after", BeforeUpdatedAt: now.Add(-time.Minute).In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05"), AfterUpdatedAt: now.In(time.FixedZone("MSK", 10800)).Format("2006-01-02 15:04:05"), ObservedAt: now, ConfigRevision: command.ConfigRevision, CredentialRevision: command.Credentials.Moysklad, Action: ActionDemandCreate, Scheme: "FBS", EventKind: "handoff_confirmed", WholePosting: true}
	var err error
	intent.Digest, err = intent.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	policy := &LifecyclePolicy{ID: "00000000-0000-4000-8000-000000000024", Scope: command.Scope, Revision: 1, ConfigRevision: command.ConfigRevision, OwnerID: "owner", Enabled: true, GrantedAt: now.Add(-time.Hour), DemandMoment: OnHandoffConfirmed, AutoDemand: true, Mappings: []MerchantCommandMapping{{StateID: after, FromStateIDs: []string{before}, Action: ActionDemandCreate, Scheme: "FBS", EventKind: "handoff_confirmed", WholePosting: true}}}
	command.ExpectedTargetRevision, command.MerchantIntent, command.LifecyclePolicyRevision = "after", &intent, 1
	command.NativeMovement.Applicable = Present(true)
	command.NativeMovement.ParentApplicable = Present(true)
	command.NativeMovement.LinkedDemandIDs = Present([]string{})
	command.NativeMovement.Event.Kind, command.NativeMovement.Event.Confirmed, command.NativeMovement.Event.OccurredAt = "shipment_dispatched", true, now
	return command, Config{Scope: command.Scope, Revision: command.ConfigRevision, NativeDocuments: &nativePolicy, Lifecycle: policy}, now
}

func TestNativeAutomaticConsentRequiresPhysicalIntentAndExactAuthority(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Command, *Config, *time.Time)
		allow  bool
	}{
		{name: "separate owner policy permits confirmed full handoff", allow: true},
		{name: "mirror grant cannot authorize shipment", change: func(c *Command, _ *Config, _ *time.Time) { c.AutomationGrantRevision = 1 }},
		{name: "missing physical declaration", change: func(c *Command, _ *Config, _ *time.Time) { c.MerchantIntent = nil }},
		{name: "packing is not handoff", change: func(c *Command, _ *Config, _ *time.Time) { c.NativeMovement.Event.Kind = "assembly_ready" }},
		{name: "unconfirmed physical event", change: func(c *Command, _ *Config, _ *time.Time) { c.NativeMovement.Event.Confirmed = false }},
		{name: "native time differs from seller revision", change: func(c *Command, _ *Config, _ *time.Time) {
			c.NativeMovement.Event.OccurredAt = c.NativeMovement.Event.OccurredAt.Add(-time.Second)
		}},
		{name: "revoked owner opt in", change: func(_ *Command, config *Config, _ *time.Time) { config.Lifecycle.Enabled = false }},
		{name: "old policy revision", change: func(c *Command, _ *Config, _ *time.Time) { c.LifecyclePolicyRevision++ }},
		{name: "credentials rotated", change: func(c *Command, _ *Config, _ *time.Time) { c.Credentials.Moysklad++ }},
		{name: "native order changed after intent", change: func(c *Command, _ *Config, _ *time.Time) { c.ExpectedTargetRevision = "newer" }},
		{name: "other customerorder", change: func(c *Command, _ *Config, _ *time.Time) {
			c.Correlation.CustomerOrderID = "00000000-0000-4000-8000-000000000025"
		}},
		{name: "other account pair", change: func(c *Command, _ *Config, _ *time.Time) { c.Scope.OzonAccount = "other" }},
		{name: "foreign policy config", change: func(_ *Command, config *Config, _ *time.Time) { config.Scope.Tenant = "other" }},
		{name: "newer config without renewed command", change: func(_ *Command, config *Config, _ *time.Time) { config.Revision++ }},
		{name: "observed state without transition", change: func(c *Command, _ *Config, _ *time.Time) {
			c.MerchantIntent.BeforeStateID = c.MerchantIntent.AfterStateID
		}},
		{name: "stale intent", change: func(_ *Command, _ *Config, now *time.Time) { *now = now.Add(16 * time.Minute) }},
		{name: "event exceeds configured age", change: func(_ *Command, config *Config, now *time.Time) {
			config.NativeDocuments.EventMaxAgeSeconds = 30
			*now = now.Add(time.Minute)
		}},
		{name: "quantity exceeds configured cap", change: func(c *Command, _ *Config, _ *time.Time) {
			c.NativeMovement.Lines[0].Quantity = "10.000000000000000001"
		}},
		{name: "native policy off", change: func(_ *Command, config *Config, _ *time.Time) { config.NativeDocuments.Enabled = false }},
		{name: "missing linked demand coverage", change: func(c *Command, _ *Config, _ *time.Time) { c.NativeMovement.LinkedDemandIDs = Field[[]string]{} }},
		{name: "null linked demands", change: func(c *Command, _ *Config, _ *time.Time) {
			c.NativeMovement.LinkedDemandIDs = Field[[]string]{Presence: PresenceNull}
		}},
		{name: "foreign draft demand", change: func(c *Command, _ *Config, _ *time.Time) {
			c.NativeMovement.LinkedDemandIDs = Present([]string{"00000000-0000-4000-8000-000000000099"})
		}},
		{name: "unposted parent not permitted", change: func(c *Command, _ *Config, _ *time.Time) {
			c.NativeMovement.ParentEffect = NativeParentUnpostedDocument
		}},
		{name: "reviewed unposted opt in cannot expose foreign demands", change: func(c *Command, config *Config, _ *time.Time) {
			c.NativeMovement.ParentEffect = NativeParentUnpostedDocument
			config.NativeDocuments.AllowPosted, config.NativeDocuments.AllowUnpostedParent = true, true
			config.NativeDocuments.AttestedAt, config.NativeDocuments.ValidUntil = c.CreatedAt.Add(-time.Hour), c.CreatedAt.Add(time.Hour)
		}},
		{name: "unverified Ozon acceptance", change: func(_ *Command, config *Config, _ *time.Time) {
			config.Lifecycle.DemandMoment = OnOzonAcceptanceConfirmed
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command, config, now := nativeAutomaticDomainFixture(t)
			if tc.change != nil {
				tc.change(&command, &config, &now)
			}
			if got := command.NativeMovement.AllowedByLifecycle(config, command, now); got != tc.allow {
				t.Fatalf("native automatic authority = %v, want %v", got, tc.allow)
			}
		})
	}
}

func TestNativeAutomaticConsentPreservesReviewedGrantSemantics(t *testing.T) {
	t.Parallel()
	command, config, now := nativeAutomaticDomainFixture(t)
	if command.NativeMovement.AllowedBy(config.NativeDocuments, command.Action, now) {
		t.Fatal("automatic consent expanded the legacy reviewed native policy")
	}
	if !command.NativeMovement.AllowedByLifecycle(config, command, now) {
		t.Fatal("separate lifecycle owner consent was not available")
	}
}
