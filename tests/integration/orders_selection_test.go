//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersAutomaticSelectionRotatesPastBlockedBatchAfterRestart(t *testing.T) {
	fixture := selectionOrdersFixture(t)
	selectionOrdersAutomation(t, &fixture)
	selectionSeedOrders(t, fixture, 101, 100)
	selectionTick(t, fixture)
	first := selectionLatestPreview(t, fixture)
	if len(first.Decisions) != 100 || slices.ContainsFunc(first.Decisions, func(decision orders.Decision) bool { return decision.Ready }) {
		t.Fatalf("first automatic batch was not durably blocked: count=%d", len(first.Decisions))
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_work`, 0)
	selectionTick(t, fixture)
	second := selectionLatestPreview(t, fixture)
	if second.ID == first.ID || len(second.Decisions) != 1 || second.Decisions[0].Posting.PostingNumber != "posting-000101" || !second.Decisions[0].Ready {
		t.Fatalf("restarted worker did not reach healthy posting 101: %+v", second.Decisions)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_work`, 1)
	selectionMarkUnknown(t, fixture)
	selectionTick(t, fixture)
	wrapped := selectionLatestPreview(t, fixture)
	if len(wrapped.Decisions) != 100 || wrapped.Decisions[0].Posting.PostingNumber != "posting-000001" {
		t.Fatalf("automatic selection did not wrap: %+v", wrapped.Decisions)
	}
	selectionTick(t, fixture)
	fenced := selectionLatestPreview(t, fixture)
	if len(fenced.Decisions) != 1 || fenced.Decisions[0].Ready || !slices.ContainsFunc(fenced.Decisions[0].Validation, func(value orders.Validation) bool { return value.Code == "target_fenced_unknown_or_pending" }) {
		t.Fatalf("wrapped automatic selection superseded UNKNOWN: %+v", fenced.Decisions)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_work`, 1)
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_work WHERE state='UNKNOWN'`, 1)
}

func selectionMarkUnknown(t testing.TB, fixture ordersStoreFixture) {
	t.Helper()
	work := ordersStoreClaim(t, fixture, time.Minute)
	allowed, err := fixture.db.GuardOrderDispatch(context.Background(), orders.DispatchGuard{Work: work, Begin: true})
	if err != nil || !allowed {
		t.Fatalf("journal synthetic uncertain intent: allowed=%v error=%v", allowed, err)
	}
	now := time.Now().UTC()
	err = fixture.db.CompleteOrderWork(context.Background(), orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken,
		ExpectedState: orders.StateDispatching, State: orders.StateUnknown, Code: "SYNTHETIC_UNKNOWN", NextRunAt: now.Add(time.Minute),
		Receipt: orders.Receipt{CommandID: work.Command.ID, State: orders.StateUnknown, ObservedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrdersBoundedSelectionExactIDsAndScopeIsolation(t *testing.T) {
	fixture := selectionOrdersFixture(t)
	selectionSeedOrders(t, fixture, 101, 0)
	for _, test := range []struct {
		name     string
		scope    orders.Scope
		numbers  []string
		wantCode error
	}{{name: "tail exact", scope: fixture.config.Scope, numbers: []string{"posting-000101", "posting-000001"}},
		{name: "unknown posting", scope: fixture.config.Scope, numbers: []string{"posting-missing"}, wantCode: orders.ErrNotFound},
		{name: "wrong tenant", scope: orders.Scope{Tenant: "other-tenant", MoyskladAccount: fixture.config.Scope.MoyskladAccount, OzonAccount: fixture.config.Scope.OzonAccount}, numbers: []string{"posting-000101"}, wantCode: orders.ErrNotFound}} {
		t.Run(test.name, func(t *testing.T) {
			review, err := fixture.db.OrderReviewContextFor(context.Background(), test.scope, test.numbers, false)
			if test.wantCode != nil {
				if !errors.Is(err, test.wantCode) {
					t.Fatalf("selection returned %v, want %v", err, test.wantCode)
				}
				return
			}
			if err != nil || len(review.Postings) != 2 || len(review.Correlations) != 2 || review.Postings[1].PostingNumber != "posting-000101" {
				t.Fatalf("exact selection read another batch: postings=%d correlations=%d error=%v", len(review.Postings), len(review.Correlations), err)
			}
		})
	}
}

func TestOrdersBoundedPreviewSaveAndApprovalRejectChangedSelectedSource(t *testing.T) {
	fixture := selectionOrdersFixture(t)
	selectionSeedOrders(t, fixture, 101, 0)
	preview := selectionManualPreview(t, fixture, "posting-000101")
	saved, err := fixture.db.SaveOrderPreview(fixture.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	changed := preview.Decisions[0].Posting
	changed.Evidence.Revision = "source-2"
	changed.Evidence.ProviderUpdatedAt = orders.Present(time.Now().UTC().Add(time.Second))
	changed.Status = orders.Present("cancelled")
	ordersStoreCommit(t, fixture, changed, "selected-source-change")
	preview.ID = "changed-unsaved-preview"
	if _, err = fixture.db.SaveOrderPreview(fixture.ctx, preview); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("bounded preview save accepted stale selected facts: %v", err)
	}
	_, err = fixture.db.ApproveOrderPreview(fixture.ctx, orders.Approval{Scope: fixture.config.Scope, PreviewID: saved.ID, Actor: fixture.session.AuditID, IdempotencyKey: "stale-selected-approval"}, time.Now())
	if !errors.Is(err, orders.ErrStale) {
		t.Fatalf("bounded approval accepted stale selected facts: %v", err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_work`, 0)
}

func selectionManualPreview(t testing.TB, fixture ordersStoreFixture, number string) orders.Preview {
	t.Helper()
	current, err := fixture.db.OrderReviewContextFor(fixture.ctx, fixture.config.Scope, []string{number}, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := fmt.Sprintf("selection-preview-%d", now.UnixNano())
	decision := ordersStoreDecision(t, current, number, id, now)
	return orders.Preview{ID: id, Scope: fixture.config.Scope, ConfigRevision: current.Config.Revision, Credentials: current.Credentials,
		Actor: fixture.session.AuditID, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Decisions: []orders.Decision{decision}}
}

func TestOrdersAutomaticSelectionCursorIgnoresManualAndPriorRevision(t *testing.T) {
	fixture := selectionOrdersFixture(t)
	selectionOrdersAutomation(t, &fixture)
	selectionSeedOrders(t, fixture, 101, 100)
	selectionTick(t, fixture)
	selectionSaveNoopPreview(t, fixture)
	manual := selectionManualPreview(t, fixture, "posting-000101")
	if _, err := fixture.db.SaveOrderPreview(fixture.ctx, manual); err != nil {
		t.Fatal(err)
	}
	review, err := fixture.db.OrderReviewContextFor(context.Background(), fixture.config.Scope, nil, true)
	if err != nil || len(review.Postings) != 1 || review.Postings[0].PostingNumber != "posting-000101" {
		t.Fatalf("manual preview advanced the automatic cursor: count=%d error=%v", len(review.Postings), err)
	}
	config, err := fixture.db.SaveOrderConfig(fixture.ctx, orders.SaveConfigRequest{Config: fixture.config, ExpectedRevision: fixture.config.Revision,
		Actor: fixture.session.AuditID, IdempotencyKey: "selection-next-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	review, err = fixture.db.OrderReviewContextFor(context.Background(), config.Scope, nil, true)
	if err != nil || len(review.Postings) != 100 || review.Postings[0].PostingNumber != "posting-000001" {
		t.Fatalf("prior configuration preview advanced the current cursor: count=%d error=%v", len(review.Postings), err)
	}
}

func selectionSaveNoopPreview(t testing.TB, fixture ordersStoreFixture) {
	t.Helper()
	preview := selectionLatestPreview(t, fixture)
	preview.ID = "automatic-noop-batch"
	preview.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	preview.ExpiresAt = preview.CreatedAt.Add(time.Minute)
	for index := range preview.Decisions {
		preview.Decisions[index].Commands = nil
		preview.Decisions[index].Validation = nil
		preview.Decisions[index].Ready = true
	}
	if _, err := fixture.db.SaveOrderPreview(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
}
