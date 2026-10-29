package moysklad

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogReadBoundedPagesPreserveProductAndVariantIdentity(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.add("product", catalogTestCreate, "Second product")
	s.add("variant", catalogTestVariant, "Parent (M)")
	c := s.writer(t)
	req := catalog.ReadRequest{Scope: catalogTestScope(), Account: catalogTestAccountRef(), Limit: 1}
	seen := make(map[string]bool, 3)
	for page := 0; page < 3; page++ {
		snap, err := c.ReadCatalog(context.Background(), req)
		if err != nil || len(snap.Products) != 1 || snap.Scope != req.Scope || snap.Account != req.Account {
			t.Fatalf("invalid bounded page: %+v %v", snap, err)
		}
		p := snap.Products[0]
		key := p.Identity.EntityType + ":" + p.Identity.ID
		if seen[key] || p.Identity.Account != req.Account || p.Revision == "" {
			t.Fatalf("duplicate, unscoped or unversioned product: %+v", p)
		}
		seen[key] = true
		if p.Variant && (p.Parent == nil || p.Parent.ID != catalogTestProduct || p.Parent.Account != req.Account) {
			t.Fatal("variant parent identity was lost")
		}
		if snap.Complete != (page == 2) || (snap.NextCursor == "") != snap.Complete {
			t.Fatalf("page completeness was fabricated: %+v", snap)
		}
		req.Cursor = snap.NextCursor
	}
}

func TestCatalogReadBudgetRejectsAnIncompleteAccountTraversal(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	for i := 20; i < 220; i++ {
		s.add("product", fmt.Sprintf("10000000-0000-0000-0000-%012d", i), "Bounded item")
	}
	s.client.pageSize = 1
	snap, err := s.writer(t).ReadCatalog(context.Background(), catalog.ReadRequest{Scope: catalogTestScope(), Account: catalogTestAccountRef(), Limit: 1000})
	var limit *LimitError
	if !errors.As(err, &limit) || len(snap.Products) != 0 || snap.Complete {
		t.Fatalf("budget exhaustion returned a partial authoritative snapshot: %+v %v", snap, err)
	}
	_, _, events := s.state()
	if len(events) != 129 {
		t.Fatalf("catalog request budget was not enforced: %d requests", len(events))
	}
}

func TestCatalogReadDistinguishesMissingEmptyAndUnexpandedValues(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.mu.Lock()
	row := s.rows["product:"+catalogTestProduct]
	row["description"], row["attributes"], row["barcodes"] = "", nil, []any{}
	row["images"] = map[string]any{"meta": map[string]any{"size": 2}}
	delete(row, "uom")
	s.mu.Unlock()
	p := catalogReadOne(t, s.writer(t), "product", catalogTestProduct)
	for _, field := range []catalog.Field{catalog.FieldDescription, catalog.FieldAttributes, catalog.FieldBarcodes} {
		if p.Fields[field].State != catalog.StateEmpty {
			t.Errorf("observed empty %s became %s", field, p.Fields[field].State)
		}
	}
	if p.Fields[catalog.FieldUnit].State != catalog.StateAbsent || p.Fields[catalog.FieldImages].State != catalog.StateUnknown {
		t.Fatal("missing or unexpanded data was represented as a known fact")
	}
	value := p.Fields[catalog.FieldName]
	if value.Provenance.Kind != catalog.EvidenceAPICurated || value.Provenance.AccountID != catalogTestAccount || value.Provenance.EntityID != catalogTestProduct || value.Provenance.Revision != p.Revision {
		t.Fatalf("API provenance is incomplete: %+v", value.Provenance)
	}
}

func TestCatalogReadRejectsForeignOrMissingIdentity(t *testing.T) {
	t.Parallel()
	for _, change := range []struct {
		name string
		edit func(*catalogSimulator)
	}{
		{"authenticated account", func(s *catalogSimulator) { s.account = catalogTestCreate }},
		{"product account", func(s *catalogSimulator) { s.rows["product:"+catalogTestProduct]["accountId"] = catalogTestCreate }},
		{"missing account", func(s *catalogSimulator) { delete(s.rows["product:"+catalogTestProduct], "accountId") }},
		{"foreign href", func(s *catalogSimulator) {
			s.rows["product:"+catalogTestProduct]["meta"] = map[string]any{"href": "https://evil.example/entity/product/" + catalogTestProduct, "type": "product"}
		}},
		{"missing parent", func(s *catalogSimulator) { delete(s.rows["variant:"+catalogTestVariant], "product") }},
		{"wrong parent type", func(s *catalogSimulator) {
			s.rows["variant:"+catalogTestVariant]["product"] = s.reference("variant", catalogTestProduct)
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			s := newCatalogSimulator(t)
			s.add("variant", catalogTestVariant, "Parent (M)")
			s.mu.Lock()
			change.edit(s)
			s.mu.Unlock()
			req := catalog.ReadRequest{Scope: catalogTestScope(), Account: catalogTestAccountRef(), Limit: 10}
			if _, err := s.writer(t).ReadCatalog(context.Background(), req); !errors.Is(err, ErrCatalogIdentity) {
				t.Fatalf("untrusted catalog identity accepted: %v", err)
			}
		})
	}
}

func TestCatalogReadValidatesScopeCursorAndBoundBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, edit := range []func(*catalog.ReadRequest){
		func(r *catalog.ReadRequest) { r.Account.ID = catalogTestCreate },
		func(r *catalog.ReadRequest) { r.Account.Provider = catalog.ProviderOzon },
		func(r *catalog.ReadRequest) { r.Cursor = "../customerorder:0" },
		func(r *catalog.ReadRequest) { r.Cursor = "product:-1" },
		func(r *catalog.ReadRequest) { r.Cursor = "product:01" },
		func(r *catalog.ReadRequest) { r.Limit = 1001 },
		func(r *catalog.ReadRequest) { r.Search = strings.Repeat("x", 256) },
	} {
		s := newCatalogSimulator(t)
		req := catalog.ReadRequest{Scope: catalogTestScope(), Account: catalogTestAccountRef()}
		edit(&req)
		if _, err := s.writer(t).ReadCatalog(context.Background(), req); err == nil {
			t.Fatal("invalid request was admitted")
		}
		_, _, events := s.state()
		if len(events) != 0 {
			t.Fatalf("invalid request reached provider: %v", events)
		}
	}
}

func TestCatalogReadOnlyClientCannotDispatchEvenOnLoopback(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, err := NewCatalogClient(s.client)
	if err != nil {
		t.Fatal(err)
	}
	cmd := catalogCreateCommand("product")
	if err := c.BindCatalogIntent(cmd, catalogAuthorize); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatalf("read-only client bound a write: %v", err)
	}
	if _, err := c.WriteCatalog(context.Background(), cmd); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatalf("read-only client dispatched a write: %v", err)
	}
	if _, err := c.ReadCatalog(context.Background(), catalog.ReadRequest{Scope: cmd.Scope, Account: cmd.Target.Account}); err != nil {
		t.Fatal(err)
	}
	writes, _, events := s.state()
	if writes != 0 {
		t.Fatal("read-only client wrote externally")
	}
	for _, event := range events {
		if !strings.HasPrefix(event, http.MethodGet+" ") || strings.Contains(event, "stock") {
			t.Fatalf("catalog reader reached unrelated API: %s", event)
		}
	}
}
