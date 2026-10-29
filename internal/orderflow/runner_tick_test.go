package orderflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type flowCollector struct {
	visited []orders.Scope
	fail    orders.Scope
}

func (c *flowCollector) Collect(_ context.Context, scope orders.Scope) error {
	c.visited = append(c.visited, scope)
	if scope == c.fail {
		return orders.ErrStale
	}
	return nil
}

func attachFlowGrant(review *orders.ReviewContext) {
	grant := orders.AutomationGrant{ID: "grant", Scope: review.Config.Scope, Revision: 3, ConfigRevision: review.Config.Revision,
		OwnerID: "stable-owner", SourceAccount: review.Config.Scope.OzonAccount, Actions: []orders.Action{orders.ActionMirrorCreate},
		Fields:    []string{"name", "state", "applicable", "positions.quantity", "positions.reserve", "positions.price"},
		GrantedAt: flowTestNow.Add(-time.Minute), ExpiresAt: flowTestNow.Add(time.Hour)}
	review.Config.Automation = &grant
}

func TestRunnerTickContinuesIndependentScopesAndApprovesOnlyGrantedReadyCommands(t *testing.T) {
	t.Parallel()
	first, second := flowReview(t, "first-posting"), flowReview(t, "second-posting")
	second = flowReviewInTenant(second, "second-tenant")
	attachFlowGrant(&first)
	attachFlowGrant(&second)
	store := &flowStore{review: second, due: []orders.Config{first.Config, second.Config}}
	collector := &flowCollector{fail: first.Config.Scope}
	service := flowService(t, store, flowResolver{})
	runner := flowRunner(t, store, flowResolver{}, service)
	runner.options.Collector = collector
	err := runner.Tick(context.Background())
	if !errors.Is(err, orders.ErrStale) || len(collector.visited) != 2 {
		t.Fatalf("failed scope prevented independent collection: visited=%v error=%v", collector.visited, err)
	}
	if !store.approval.Automatic || store.approval.Actor != "AUTOMATION:grant:3" || !slices.Equal(store.approval.PostingNumbers, []string{"second-posting"}) {
		t.Fatalf("automatic approval lost stable grant authority: %+v", store.approval)
	}
	for _, preview := range store.previews {
		if !preview.Automatic || preview.Decisions[0].Commands[0].AutomationGrantRevision != 3 {
			t.Fatal("scheduled command was not bound to the recorded grant revision")
		}
	}
}

func flowReviewInTenant(review orders.ReviewContext, tenant string) orders.ReviewContext {
	review.Config.Scope.Tenant = tenant
	for index := range review.Postings {
		review.Postings[index].Scope.Tenant = tenant
	}
	for index := range review.ProductMappings {
		review.ProductMappings[index].Scope.Tenant = tenant
	}
	for index := range review.WarehouseMappings {
		review.WarehouseMappings[index].Scope.Tenant = tenant
	}
	review.Correlations = nil
	return review
}

func TestRunnerTickExpiredGrantStopsReadsAndUnpermittedFieldsStopApproval(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		change  func(*orders.AutomationGrant)
		collect bool
	}{{name: "expired", change: func(grant *orders.AutomationGrant) { grant.ExpiresAt = flowTestNow }},
		{name: "field not granted", collect: true, change: func(grant *orders.AutomationGrant) { grant.Fields = []string{"state"} }}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "posting")
			attachFlowGrant(&review)
			test.change(review.Config.Automation)
			store := &flowStore{review: review, due: []orders.Config{review.Config}}
			collector := &flowCollector{}
			runner := flowRunner(t, store, flowResolver{}, flowService(t, store, flowResolver{}))
			runner.options.Collector = collector
			if err := runner.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (len(collector.visited) == 1) != test.collect || store.approval.PreviewID != "" {
				t.Fatalf("grant did not fence background work: visited=%d approval=%+v", len(collector.visited), store.approval)
			}
		})
	}
}

func TestRunnerTickDoesNotPersistCookiesAsAutomationActors(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	attachFlowGrant(&review)
	store := &flowStore{review: review, due: []orders.Config{review.Config}}
	runner := flowRunner(t, store, flowResolver{}, flowService(t, store, flowResolver{}))
	runner.options.Collector = &flowCollector{}
	if err := runner.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(store.approval.Actor, "AUTOMATION:") || store.approval.Actor == review.Config.Automation.OwnerID {
		t.Fatal("worker reused an interactive session or owner ID as an authentication cookie")
	}
}
