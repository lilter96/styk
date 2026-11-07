package orders

import (
	"testing"
	"time"
)

func merchantTransitionFixture(t *testing.T) (NativeOrderPoint, NativeOrderPoint, Config, NativeStateBaseline, time.Time) {
	t.Helper()
	command, config, now := nativeAutomaticDomainFixture(t)
	intent := command.MerchantIntent
	point := NativeOrderPoint{Scope: command.Scope, Correlation: command.Correlation, CredentialRevision: command.Credentials.Moysklad, StartedAt: now.Add(-time.Minute), FinishedAt: now.Add(-time.Minute + time.Second), ObservedAt: now.Add(-time.Minute), TargetRevision: intent.BeforeRevision, StoreID: command.Warehouse.MoyskladStoreID, StateID: intent.BeforeStateID, ProviderUpdatedAt: Present(intent.BeforeUpdatedAt)}
	after := point
	after.StartedAt, after.FinishedAt, after.ObservedAt = now.Add(-time.Second), now, now
	after.StateID, after.TargetRevision, after.ProviderUpdatedAt = intent.AfterStateID, intent.AfterRevision, Present(intent.AfterUpdatedAt)
	baseline := NativeStateBaseline{Scope: command.Scope, Correlation: command.Correlation, WorkID: "mirror", CommandID: "create", StateID: intent.BeforeStateID, TargetRevision: "initial", ObservedAt: now.Add(-time.Hour)}
	return point, after, config, baseline, now
}

func TestMerchantTransitionRequiresObservedChangeAndProvenance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*NativeOrderPoint, *NativeOrderPoint, *Config, *NativeStateBaseline)
		allow  bool
	}{
		{name: "new physical seller declaration", allow: true},
		{name: "same state is not a transition", change: func(b, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) { a.StateID = b.StateID }},
		{name: "same revision is not a transition", change: func(b, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) { a.TargetRevision = b.TargetRevision }},
		{name: "old update cannot replay", change: func(b, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) {
			a.ProviderUpdatedAt = b.ProviderUpdatedAt
		}},
		{name: "unknown source update", change: func(_, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) {
			a.ProviderUpdatedAt = Field[string]{}
		}},
		{name: "own forward echo", change: func(_, a *NativeOrderPoint, _ *Config, own *NativeStateBaseline) { own.StateID = a.StateID }},
		{name: "foreign document", change: func(_, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) {
			a.Correlation.CustomerOrderID = "foreign"
		}},
		{name: "deleted native order", change: func(_, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) {
			a.SourceDeleted = Present("2026-11-06 16:00:00")
		}},
		{name: "rotated credential seeds baseline", change: func(_, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) { a.CredentialRevision++ }},
		{name: "overlapping readers cannot infer ordering", change: func(b, a *NativeOrderPoint, _ *Config, _ *NativeStateBaseline) { a.StartedAt = b.StartedAt }},
		{name: "consent revoked", change: func(_, _ *NativeOrderPoint, c *Config, _ *NativeStateBaseline) { c.Lifecycle.Enabled = false }},
		{name: "no owned producer", change: func(_, _ *NativeOrderPoint, _ *Config, own *NativeStateBaseline) { *own = NativeStateBaseline{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before, after, config, own, now := merchantTransitionFixture(t)
			if tc.change != nil {
				tc.change(&before, &after, &config, &own)
			}
			intent, found := ObserveMerchantTransition(before, after, config, own, "FBS", now)
			if found != tc.allow {
				t.Fatalf("transition admitted=%v want=%v", found, tc.allow)
			}
			if found && intent.Validate() != nil {
				t.Fatal("invalid durable intent")
			}
		})
	}
}

func TestMerchantTransitionScopeSchemeAndReplayIdentity(t *testing.T) {
	t.Parallel()
	before, after, config, own, now := merchantTransitionFixture(t)
	first, found := ObserveMerchantTransition(before, after, config, own, "FBS", now)
	if !found {
		t.Fatal("valid transition missing")
	}
	after.StartedAt, after.FinishedAt, after.ObservedAt = after.StartedAt.Add(time.Second), after.FinishedAt.Add(time.Second), after.ObservedAt.Add(time.Second)
	replay, found := ObserveMerchantTransition(before, after, config, own, "FBS", now.Add(time.Second))
	if !found || first.Digest != replay.Digest || first.ID != replay.ID {
		t.Fatal("read clock manufactured a different business event")
	}
	if _, found = ObserveMerchantTransition(before, after, config, own, "rFBS", now.Add(time.Second)); found {
		t.Fatal("state inferred a different delivery scheme")
	}
}

func TestLifecycleMappingsRejectForwardEchoAndCycles(t *testing.T) {
	t.Parallel()
	_, _, config, _, _ := merchantTransitionFixture(t)
	config.Enabled, config.TestEnabled = true, true
	if err := config.validateLifecycle(); err != nil {
		t.Fatal(err)
	}
	config.StatusMappings = []StatusMapping{{StateID: config.Lifecycle.Mappings[0].StateID}}
	if config.validateLifecycle() == nil {
		t.Fatal("forward echo target accepted")
	}
	config.StatusMappings = nil
	first := config.Lifecycle.Mappings[0]
	second := first
	second.StateID, second.FromStateIDs = first.FromStateIDs[0], []string{first.StateID}
	config.Lifecycle.Mappings = append(config.Lifecycle.Mappings, second)
	if config.validateLifecycle() == nil {
		t.Fatal("cyclic native command mapping accepted")
	}
}

func TestMerchantPhysicalEventCanJournalNativeAndCarrierCommandsSeparately(t *testing.T) {
	t.Parallel()
	before, after, config, own, now := merchantTransitionFixture(t)
	config.Lifecycle.Mappings[0].Scheme = "rFBS"
	carrier := config.Lifecycle.Mappings[0]
	carrier.Action = ActionPostingShip
	config.Lifecycle.Mappings = append(config.Lifecycle.Mappings, carrier)
	if err := config.Lifecycle.Validate(); err != nil {
		t.Fatal(err)
	}
	intents := ObserveMerchantTransitions(before, after, config, own, "rFBS", now)
	if len(intents) != 2 || intents[0].Digest == intents[1].Digest {
		t.Fatal("separate command identity missing", intents)
	}
	if intents[0].AfterRevision != intents[1].AfterRevision {
		t.Fatal("commands lost their common physical transition")
	}
}
