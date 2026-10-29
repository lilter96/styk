//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

func selectionOrdersFixture(t testing.TB) ordersStoreFixture {
	t.Helper()
	raw, err := os.ReadFile("../../.local/v7/test-db-dsn")
	if err != nil {
		t.Fatal("dedicated selection test database unavailable")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	if err != nil || u.Scheme != "postgres" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Path != "/styk_orders_store_test" {
		t.Fatal("selection tests require the dedicated localhost orders test database")
	}
	t.Setenv("TEST_DATABASE_URL", u.String())
	return newOrdersStoreFixture(t)
}

func selectionOrdersAutomation(t testing.TB, fixture *ordersStoreFixture) {
	t.Helper()
	config := fixture.config
	config.CurrencyID = "11111111-1111-4111-8111-111111111111"
	config.Automation = &orders.AutomationGrant{ExpiresAt: time.Now().Add(time.Hour), Actions: []orders.Action{orders.ActionMirrorCreate},
		Fields: []string{"name", "state", "positions.quantity", "positions.price", "positions.vat"}}
	updated, err := fixture.db.SaveOrderConfig(fixture.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision,
		Actor: fixture.session.AuditID, IdempotencyKey: "selection-automation"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fixture.config = updated
}

func selectionSeedOrders(t testing.TB, fixture ordersStoreFixture, size, blocked int) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := ordersIngestionLease(t, fixture, "selection", "initial", 5*time.Minute)
	var revision int64
	cursor := ""
	for start := 0; start < size; start += orders.MaxPagePostings {
		end := min(start+orders.MaxPagePostings, size)
		postings := selectionPostings(fixture.config.Scope, now, start, end, blocked)
		next := fmt.Sprint(end)
		if end == size {
			next = ""
		}
		page := orders.SourcePage{Scope: fixture.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: lease.Stream,
			RefreshKey: lease.RefreshKey, ExpectedCheckpointRevision: revision, ExpectedCursor: cursor, NextCursor: next, Complete: end == size,
			WindowFrom: now.Add(-time.Hour), WindowTo: now, Postings: postings, StartedAt: now, FinishedAt: now,
			IdempotencyKey: fmt.Sprintf("selection-page-%d", start), FenceToken: lease.Token}
		checkpoint, err := fixture.db.CommitOrderPage(fixture.ctx, page)
		if err != nil {
			t.Fatal(err)
		}
		revision, cursor = checkpoint.Revision, checkpoint.Cursor
	}
}

func selectionPostings(scope orders.Scope, now time.Time, start, end, blocked int) []orders.Posting {
	postings := make([]orders.Posting, 0, end-start)
	for index := start; index < end; index++ {
		posting := ordersStorePosting(scope, fmt.Sprintf("posting-%06d", index+1), "source-1", now)
		posting.Scheme = orders.Present("FBS")
		if index < blocked {
			posting.Lines[0].Identity.OfferID = "unmapped-offer"
		}
		postings = append(postings, posting)
	}
	return postings
}

type selectionNoProvider struct{}

func (selectionNoProvider) ResolveOrder(context.Context, orders.Scope, string, string, int64) (orders.Binding, error) {
	return orders.Binding{}, orders.ErrBlocked
}

type selectionNoCollection struct{}

func (selectionNoCollection) Collect(context.Context, orders.Scope) error { return nil }

func selectionRunner(t testing.TB, fixture ordersStoreFixture) *orderflow.Runner {
	t.Helper()
	allowed := func(scope orders.Scope) bool { return scope == fixture.config.Scope }
	service, err := orderflow.NewService(fixture.db, selectionNoProvider{}, orderflow.ServiceOptions{AllowScope: allowed})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := orderflow.NewRunner(fixture.db, selectionNoProvider{}, orderflow.RunnerOptions{WorkerID: "selection-worker", Service: service,
		Collector: selectionNoCollection{}, AllowScope: allowed})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func selectionTick(t testing.TB, fixture ordersStoreFixture) {
	t.Helper()
	if _, err := fixture.db.Pool.Exec(context.Background(), `UPDATE orders_configs SET next_run_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, fixture.config.Scope.Tenant, fixture.config.Scope.MoyskladAccount, fixture.config.Scope.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if err := selectionRunner(t, fixture).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func selectionLatestPreview(t testing.TB, fixture ordersStoreFixture) orders.Preview {
	t.Helper()
	var id string
	scope := fixture.config.Scope
	err := fixture.db.Pool.QueryRow(context.Background(), `SELECT id FROM orders_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.db.OrderPreview(context.Background(), scope, id)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}
