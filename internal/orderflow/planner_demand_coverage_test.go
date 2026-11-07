package orderflow

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type demandCoverageProvider struct {
	*flowProvider
	read  func(orders.TargetObservation) (orders.NativeDemandCoverage, error)
	calls int
}

func (p *demandCoverageProvider) ReadNativeDemandCoverage(_ context.Context, parent orders.TargetObservation) (orders.NativeDemandCoverage, error) {
	p.calls++
	return p.read(parent)
}

func TestNativeDemandPlannerUsesCoverageFinishedAfterInitialPlanningClock(t *testing.T) {
	t.Parallel()
	clock := flowTestNow
	parent := orders.TargetObservation{Scope: flowReview(t, "100-1").Config.Scope, Revision: "parent-revision"}
	parent.Correlation.Scope = parent.Scope
	provider := &demandCoverageProvider{flowProvider: &flowProvider{}}
	provider.read = func(parent orders.TargetObservation) (orders.NativeDemandCoverage, error) {
		start := clock
		clock = clock.Add(time.Second)
		return orders.NativeDemandCoverage{Scope: parent.Scope, Correlation: parent.Correlation, ParentRevision: parent.Revision, Visibility: "ALL", Complete: true, DemandIDs: []string{}, StartedAt: start, FinishedAt: clock}, nil
	}
	coverage, issue := planNativeDemandCoverage(context.Background(), provider, parent, true, func() time.Time { return clock })
	if issue.Code != "" || coverage == nil || !coverage.FinishedAt.After(flowTestNow) || provider.calls != 1 {
		t.Fatal("valid provider evidence was rejected using pre-read planning clock", issue)
	}
}

func TestNativeDemandPlannerKeepsRawLinkAndCapabilityFences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		links     orders.Field[[]string]
		automatic bool
		want      string
	}{
		{name: "manual demand does not require automatic coverage"},
		{name: "explicit empty avoids account scan", links: orders.Present([]string{}), automatic: true},
		{name: "existing raw draft never erased", links: orders.Present([]string{"draft"}), automatic: true, want: "native_existing_linked_demand_requires_review"},
		{name: "unsupported reader leaves missing unknown", automatic: true, want: "native_linked_demand_coverage_unavailable"},
		{name: "unsupported reader leaves null unknown", links: orders.Field[[]string]{Presence: orders.PresenceNull}, automatic: true, want: "native_linked_demand_coverage_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coverage, issue := planNativeDemandCoverage(context.Background(), &flowProvider{}, orders.TargetObservation{LinkedDemandIDs: tc.links}, tc.automatic, time.Now)
			if issue.Code != tc.want || coverage != nil {
				t.Fatal("raw demand fence changed", issue)
			}
		})
	}
}
