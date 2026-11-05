package postgres

import (
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderPostingCursorBindsFiltersAndScope(t *testing.T) {
	t.Parallel()
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	page := orders.PageRequest{Limit: 25, Search: "order", Status: "delivered", Attention: true}
	cursor, err := orderPageCursor(scope, page, "postings")
	if err != nil {
		t.Fatal(err)
	}
	cursor.ID = "last-posting"
	page.Cursor, err = encodeOrdersCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*orders.Scope, *orders.PageRequest)
		valid  bool
	}{
		{"unchanged", func(*orders.Scope, *orders.PageRequest) {}, true},
		{"status", func(_ *orders.Scope, p *orders.PageRequest) { p.Status = "cancelled" }, false},
		{"attention", func(_ *orders.Scope, p *orders.PageRequest) { p.Attention = false }, false},
		{"search", func(_ *orders.Scope, p *orders.PageRequest) { p.Search = "other" }, false},
		{"tenant", func(s *orders.Scope, _ *orders.PageRequest) { s.Tenant = "other" }, false},
		{"account", func(s *orders.Scope, _ *orders.PageRequest) { s.OzonAccount = "other" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, p := scope, page
			tc.change(&s, &p)
			_, err := orderPageCursor(s, p, "postings")
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, orders.ErrInvalid) {
				t.Fatalf("substituted cursor accepted: %v", err)
			}
		})
	}
}

func TestOrderPostingDefaultCursorRetainsPreviousBinding(t *testing.T) {
	t.Parallel()
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	previous, err := ordersHash(struct {
		Scope  orders.Scope
		Search string
		Kind   string
	}{scope, "query", "postings"})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := orderPageCursor(scope, orders.PageRequest{Limit: 25, Search: "query"}, "postings")
	if err != nil || cursor.Binding != previous {
		t.Fatalf("unfiltered cursor binding changed: %v", err)
	}
}
