package orders

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAutomaticDemandCoverageBindsCompleteAccountEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*NativeMovement, *Command, *time.Time)
		want string
	}{
		{name: "missing header complete account scan"},
		{name: "null header complete account scan", edit: func(m *NativeMovement, _ *Command, _ *time.Time) {
			m.LinkedDemandIDs = Field[[]string]{Presence: PresenceNull}
		}},
		{name: "existing raw link cannot be erased", want: "native_existing_linked_demand_requires_review", edit: func(m *NativeMovement, _ *Command, _ *time.Time) { m.LinkedDemandIDs = Present([]string{"foreign"}) }},
		{name: "existing scanned draft", want: "native_existing_linked_demand_requires_review", edit: func(m *NativeMovement, _ *Command, _ *time.Time) {
			m.DemandCoverage.DemandIDs = []string{"draft"}
			m.DemandCoverage.AccountRows = 1
		}},
		{name: "foreign account", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) { m.DemandCoverage.Scope.MoyskladAccount = "foreign" }},
		{name: "different parent", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) {
			m.DemandCoverage.Correlation.CustomerOrderID = "foreign"
		}},
		{name: "different revision", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) { m.DemandCoverage.ParentRevision = "changed" }},
		{name: "scope own hides demands", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) { m.DemandCoverage.Visibility = "OWN" }},
		{name: "truncated", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) { m.DemandCoverage.Complete = false }},
		{name: "null evidence rows", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) { m.DemandCoverage.DemandIDs = nil }},
		{name: "expired", want: "native_linked_demand_coverage_unavailable", edit: func(_ *NativeMovement, _ *Command, now *time.Time) { *now = now.Add(6 * time.Minute) }},
		{name: "future clock", want: "native_linked_demand_coverage_unavailable", edit: func(m *NativeMovement, _ *Command, _ *time.Time) {
			m.DemandCoverage.FinishedAt = m.DemandCoverage.FinishedAt.Add(time.Minute)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command, config, now := nativeAutomaticDomainFixture(t)
			movement := command.NativeMovement
			movement.LinkedDemandIDs = Field[[]string]{}
			movement.DemandCoverage = &NativeDemandCoverage{Scope: command.Scope, Correlation: command.Correlation, ParentRevision: command.ExpectedTargetRevision, DemandIDs: []string{}, Visibility: "ALL", Complete: true, StartedAt: now.Add(-time.Second), FinishedAt: now}
			if tc.edit != nil {
				tc.edit(movement, &command, &now)
			}
			if got := movement.DemandCoverageValidation(command, now).Code; got != tc.want {
				t.Fatalf("code=%s want=%s", got, tc.want)
			}
			if tc.want == "" && !movement.AllowedByLifecycle(config, command, now) {
				t.Fatal("valid account evidence did not pass lifecycle quantity/consent guard")
			}
		})
	}
}

func TestLegacyNativeCommandOmitsAbsentDerivedCoverage(t *testing.T) {
	t.Parallel()
	command, _, _ := nativeAutomaticDomainFixture(t)
	raw, err := json.Marshal(command)
	if err != nil || strings.Contains(string(raw), "demand_coverage") {
		t.Fatal("legacy command hash acquired an absent evidence field", err)
	}
}
