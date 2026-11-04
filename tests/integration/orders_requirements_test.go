//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersPackingRequirementsPersistMissingAndExplicitResolution(t *testing.T) {
	f := newOrdersStoreFixture(t)
	baseline := time.Now().UTC().Add(-time.Hour)
	before := ordersStorePosting(f.config.Scope, "requirements-posting", "initial", baseline)
	before.Requirements.Products[orders.RequirementMandatoryMark] = orders.Present([]string{"2001"})
	ordersStoreCommit(t, f, before, "requirements-initial")
	for i, tc := range []struct {
		name         string
		requirements *orders.PackingRequirements
		known        bool
	}{
		{"missing", nil, true},
		{"null", &orders.PackingRequirements{Presence: orders.PresenceNull}, true},
		{"explicit resolution", ordersStorePosting(f.config.Scope, "fixture", "fixture", baseline).Requirements, false},
	} {
		next := before
		next.Requirements = tc.requirements
		next.Evidence.Revision = tc.name
		next.Evidence.ProviderUpdatedAt = orders.Present(baseline.Add(time.Duration(i+1) * time.Second))
		ordersStoreCommit(t, f, next, "requirements-"+tc.name)
		review, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
		if err != nil || len(review.Postings) != 1 || review.Postings[0].Requirements == nil {
			t.Fatalf("read persisted requirements: %v", err)
		}
		field := review.Postings[0].Requirements.Products[orders.RequirementMandatoryMark]
		if field.IsKnown() != tc.known || (!tc.known && field.Presence != orders.PresenceEmpty) {
			t.Fatalf("%s lost requirement semantics: %+v", tc.name, field)
		}
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_posting_observations`, 4)
}
