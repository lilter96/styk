package moysklad

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogCreateLookupUsesOnlyExactDurableMarker(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, err := NewCatalogClient(s.client)
	if err != nil {
		t.Fatal(err)
	}
	cmd := catalog.Command{Scope: catalogTestScope(), Target: catalog.Identity{Account: catalogTestAccountRef(), EntityType: "product"}, Action: catalog.ActionCreate, CreateExternalCode: "styk-exact-creation-marker"}
	missing, err := c.LookupCatalogCreate(context.Background(), cmd)
	if err != nil || !missing.Complete || len(missing.Products) != 0 {
		t.Fatalf("exact absence observation failed: %+v %v", missing, err)
	}
	s.mu.Lock()
	s.rows["product:"+catalogTestProduct]["externalCode"] = cmd.CreateExternalCode
	s.mu.Unlock()
	found, err := c.LookupCatalogCreate(context.Background(), cmd)
	if err != nil || !found.Complete || len(found.Products) != 1 || found.Products[0].Identity.ID != catalogTestProduct || found.Scope != cmd.Scope || found.Account != cmd.Target.Account {
		t.Fatalf("exact existing identity was not returned: %+v %v", found, err)
	}
	writes, _, _ := s.state()
	if writes != 0 {
		t.Fatal("creation lookup performed a mutation")
	}
}

func TestCatalogCreateLookupRejectsDuplicateMarkerAndFilterInjection(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.add("product", catalogTestCreate, "Other")
	c, cmd := s.writer(t), catalogCreateCommand("product")
	s.mu.Lock()
	for _, row := range s.rows {
		row["externalCode"] = cmd.CreateExternalCode
	}
	s.mu.Unlock()
	if _, err := c.LookupCatalogCreate(context.Background(), cmd); !errors.Is(err, ErrCatalogWriteUnknown) {
		t.Fatalf("ambiguous marker was resolved arbitrarily: %v", err)
	}
	_, _, before := s.state()
	cmd.CreateExternalCode = "marker;externalCode=another"
	if _, err := c.LookupCatalogCreate(context.Background(), cmd); !errors.Is(err, ErrCatalogInvalid) {
		t.Fatalf("marker filter injection was admitted: %v", err)
	}
	_, _, after := s.state()
	if len(before) != len(after) {
		t.Fatal("invalid marker reached provider")
	}
}
