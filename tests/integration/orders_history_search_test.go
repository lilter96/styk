//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersHistorySearchIsExactAndCursorBound(t *testing.T) {
	f := newOrdersStoreFixture(t)
	for i, number := range []string{"posting-1", "posting-10"} {
		p := ordersStorePosting(f.config.Scope, number, "source-"+number, time.Now().UTC().Add(time.Duration(i)*time.Microsecond))
		ordersStoreCommit(t, f, p, "history-search-"+number)
		ordersStoreApprove(t, f, ordersStorePreview(t, f, number))
	}
	all, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 1})
	if err != nil || len(all.Operations) != 1 || all.NextCursor == "" {
		t.Fatalf("unfiltered pagination: %v", err)
	}
	page, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 1, Search: "posting-1"})
	if err != nil || len(page.Operations) != 1 || page.NextCursor != "" || page.Operations[0].Targets[0].Command.PostingNumber != "posting-1" {
		t.Fatalf("exact posting search: %v", err)
	}
	_, err = f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 1, Search: "posting-1", Cursor: all.NextCursor})
	if err == nil {
		t.Fatal("unfiltered cursor reused for different selection")
	}
	page, err = f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10, Search: "' OR 1=1 --"})
	if err != nil || len(page.Operations) != 0 {
		t.Fatal("posting search escaped parameter scope")
	}
}
