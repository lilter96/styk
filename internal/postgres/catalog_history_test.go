package postgres

import (
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func TestCatalogHistoryCursorBindsEveryFilter(t *testing.T) {
	t.Parallel()
	scope := catalog.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	page := catalogsync.PageRequest{Limit: 20, Search: "name", State: "UNKNOWN", ProductProvider: catalog.ProviderOzon, ProductID: "1001"}
	cursor, err := catalogHistoryCursor(scope, page)
	if err != nil {
		t.Fatal(err)
	}
	page.Cursor, err = encodeCatalogCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*catalogsync.PageRequest)
	}{
		{"search", func(p *catalogsync.PageRequest) { p.Search = "other" }},
		{"state", func(p *catalogsync.PageRequest) { p.State = "RECONCILED" }},
		{"provider", func(p *catalogsync.PageRequest) { p.ProductProvider = catalog.ProviderMoySklad }},
		{"identity", func(p *catalogsync.PageRequest) { p.ProductID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			changed := page
			tc.change(&changed)
			if _, err := catalogHistoryCursor(scope, changed); !errors.Is(err, catalogsync.ErrInvalid) {
				t.Fatalf("changed filter accepted: %v", err)
			}
		})
	}
	if _, err = catalogHistoryCursor(scope, page); err != nil {
		t.Fatalf("unchanged cursor: %v", err)
	}
}
