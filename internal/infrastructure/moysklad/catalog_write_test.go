package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogCreateReadAndMarkerReuse(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, cmd := s.writer(t), catalogCreateCommand("product")
	var authorizations atomic.Int32
	catalogBind(t, c, cmd, func(context.Context) (bool, error) { authorizations.Add(1); return true, nil })
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied || r.ExternalID != catalogTestCreate || authorizations.Load() != 1 {
		t.Fatalf("creation not verified: %+v %v", r, err)
	}
	p := catalogReadOne(t, c, "product", r.ExternalID)
	if !catalog.EqualValue(p.Fields[catalog.FieldName], cmd.Values[catalog.FieldName]) || p.Parent != nil || p.Variant {
		t.Fatalf("created identity or fields differ: %+v", p)
	}
	again := s.writer(t)
	catalogBind(t, again, cmd, func(context.Context) (bool, error) {
		t.Error("already applied marker reached dispatch authorization")
		return false, nil
	})
	r, err = again.WriteCatalog(context.Background(), cmd)
	writes, bodies, _ := s.state()
	if err != nil || r.State != catalog.ReceiptApplied || writes != 1 || bodies[0]["syncId"] != cmd.CreateID || bodies[0]["id"] != nil {
		t.Fatalf("durable marker did not prevent duplicate creation: %+v writes=%d error=%v", r, writes, err)
	}
}

func TestCatalogVariantCreatePreservesActualParent(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, cmd := s.writer(t), catalogCreateCommand("variant")
	cmd.Parent = &catalog.Identity{Account: catalogTestAccountRef(), EntityType: "product", ID: catalogTestProduct}
	delete(cmd.Values, catalog.FieldName)
	cmd.Extensions = map[string]catalog.Value{"moysklad.characteristics": {State: catalog.StateKnown, Data: json.RawMessage(`[{"id":"` + catalogTestAttr + `","value":"M"}]`)}}
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied {
		t.Fatalf("variant not created: %+v %v", r, err)
	}
	p := catalogReadOne(t, c, "variant", r.ExternalID)
	if !p.Variant || p.Parent == nil || *p.Parent != *cmd.Parent {
		t.Fatalf("variant was flattened or its parent fabricated: %+v", p)
	}
	writes, bodies, _ := s.state()
	if writes != 1 || bodies[0]["syncId"] != nil || bodies[0]["id"] != nil {
		t.Fatal("variant creation used undocumented provider idempotency")
	}
}

func TestCatalogNameDescriptionUpdatePreservesUnownedFields(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c := s.writer(t)
	untouched := s.unownedJSON(t, "product", catalogTestProduct)
	before := catalogReadOne(t, c, "product", catalogTestProduct)
	cmd := catalogUpdateCommand(before)
	catalogBind(t, c, cmd, func(context.Context) (bool, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.events = append(s.events, "authorize")
		return true, nil
	})
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied {
		t.Fatalf("update failed: %+v %v", r, err)
	}
	after := catalogReadOne(t, c, "product", catalogTestProduct)
	assertCatalogUnownedFields(t, before, after)
	if untouched != s.unownedJSON(t, "product", catalogTestProduct) {
		t.Fatal("the provider's unowned fields changed")
	}
	writes, bodies, events := s.state()
	if writes != 1 || len(bodies[0]) != 2 || bodies[0]["name"] != "Created item" || bodies[0]["description"] != "Created description" {
		t.Fatalf("update payload included unselected fields: %+v", bodies)
	}
	assertCatalogImmediateAuthorization(t, events)
}

func assertCatalogUnownedFields(t *testing.T, before, after catalog.Product) {
	t.Helper()
	for _, field := range []catalog.Field{catalog.FieldArticle, catalog.FieldPrice, catalog.FieldBarcodes, catalog.FieldCategory, catalog.FieldAttributes, catalog.FieldUnit} {
		if !catalog.EqualValue(before.Fields[field], after.Fields[field]) {
			t.Errorf("unowned field %s changed", field)
		}
	}
	if before.Identity != after.Identity || before.Lifecycle != after.Lifecycle {
		t.Error("update changed product identity or lifecycle")
	}
}

func assertCatalogImmediateAuthorization(t *testing.T, events []string) {
	t.Helper()
	for i, event := range events {
		if strings.HasPrefix(event, "PUT ") && (i == 0 || events[i-1] != "authorize") {
			t.Fatalf("another request ran between authorization and dispatch: %v", events)
		}
	}
}

func TestCatalogUpdateNoopMakesNoMutation(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c := s.writer(t)
	p := catalogReadOne(t, c, "product", catalogTestProduct)
	cmd := catalogUpdateCommand(p)
	cmd.Values = map[catalog.Field]catalog.Value{catalog.FieldName: p.Fields[catalog.FieldName], catalog.FieldDescription: p.Fields[catalog.FieldDescription]}
	catalogBind(t, c, cmd, func(context.Context) (bool, error) { t.Error("no-op dispatched"); return false, nil })
	r, err := c.WriteCatalog(context.Background(), cmd)
	writes, _, _ := s.state()
	if err != nil || r.State != catalog.ReceiptApplied || writes != 0 {
		t.Fatalf("no-op wrote externally: %+v writes=%d error=%v", r, writes, err)
	}
	readback := catalogReadOne(t, c, "product", catalogTestProduct)
	if readback.Revision != p.Revision || !catalog.EqualValue(readback.Fields[catalog.FieldName], p.Fields[catalog.FieldName]) {
		t.Fatal("read time changed a catalog fact or revision")
	}
}

func TestCatalogAmbiguousWritesAreSingleAttemptAndExplicitlyReconciled(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"500", "429", "malformed", "oversized", "redirect", "disconnect", "wrong-account", "wrong-id"} {
		t.Run(mode, func(t *testing.T) { assertCatalogAmbiguousWrite(t, mode) })
	}
}

func assertCatalogAmbiguousWrite(t *testing.T, mode string) {
	t.Helper()
	s := newCatalogSimulator(t)
	s.mode = mode
	c, cmd := s.writer(t), catalogCreateCommand("product")
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	if !errors.Is(err, ErrCatalogWriteUnknown) || r.State != catalog.ReceiptUnknown {
		t.Fatalf("ambiguous outcome reported as settled: %+v %v", r, err)
	}
	if _, err := c.WriteCatalog(context.Background(), cmd); !errors.Is(err, ErrCatalogWritesDisabled) {
		t.Fatalf("uncertain write admitted a second dispatch: %v", err)
	}
	r, err = c.ReconcileCatalog(context.Background(), cmd)
	writes, _, _ := s.state()
	if err != nil || r.State != catalog.ReceiptApplied || writes != 1 {
		t.Fatalf("explicit reconciliation wrote or failed: %+v writes=%d error=%v", r, writes, err)
	}
}

func TestCatalogUnknownWithAbsentMarkerNeverWritesDuringReconciliation(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	r, err := s.writer(t).ReconcileCatalog(context.Background(), catalogCreateCommand("product"))
	writes, _, _ := s.state()
	if !errors.Is(err, ErrCatalogWriteUnknown) || r.State != catalog.ReceiptUnknown || writes != 0 {
		t.Fatalf("absence was treated as safe to resend: %+v writes=%d error=%v", r, writes, err)
	}
}

func TestCatalogFrozenIntentRejectsAnyChangedCommand(t *testing.T) {
	t.Parallel()
	for _, change := range []struct {
		name string
		edit func(*catalog.Command)
	}{
		{"field", func(cmd *catalog.Command) { cmd.Values[catalog.FieldName] = catalog.Text("Changed after journal") }},
		{"scope", func(cmd *catalog.Command) { cmd.Scope.Tenant = "other-tenant" }},
		{"marker", func(cmd *catalog.Command) { cmd.CreateExternalCode = "another-marker" }},
		{"source", func(cmd *catalog.Command) { cmd.Source.ID = "another-product" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			s := newCatalogSimulator(t)
			c, cmd := s.writer(t), catalogCreateCommand("product")
			catalogBind(t, c, cmd, catalogAuthorize)
			change.edit(&cmd)
			r, err := c.WriteCatalog(context.Background(), cmd)
			writes, _, _ := s.state()
			if !errors.Is(err, ErrCatalogWritesDisabled) || r.State != catalog.ReceiptRejected || writes != 0 {
				t.Fatalf("changed intent dispatched: %+v writes=%d error=%v", r, writes, err)
			}
		})
	}
}

func TestCatalogAuthorizationRevokedBeforeDispatch(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, cmd := s.writer(t), catalogCreateCommand("product")
	catalogBind(t, c, cmd, func(context.Context) (bool, error) { return false, nil })
	r, err := c.WriteCatalog(context.Background(), cmd)
	writes, _, _ := s.state()
	if !errors.Is(err, ErrCatalogWritesDisabled) || r.State != catalog.ReceiptRejected || writes != 0 {
		t.Fatalf("revoked intent reached provider mutation: %+v writes=%d error=%v", r, writes, err)
	}
}

func TestCatalogChangedRevisionRejectsDispatch(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c := s.writer(t)
	cmd := catalogUpdateCommand(catalogReadOne(t, c, "product", catalogTestProduct))
	catalogBind(t, c, cmd, catalogAuthorize)
	s.mu.Lock()
	s.rows["product:"+catalogTestProduct]["description"] = "Another operator edited this"
	s.mu.Unlock()
	r, err := c.WriteCatalog(context.Background(), cmd)
	writes, _, _ := s.state()
	if !errors.Is(err, ErrCatalogConflict) || r.State != catalog.ReceiptRejected || writes != 0 {
		t.Fatalf("stale revision dispatched: %+v writes=%d error=%v", r, writes, err)
	}
}

func TestCatalogDuplicateMarkerStaysUnknown(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.add("product", catalogTestCreate, "Other")
	c, cmd := s.writer(t), catalogCreateCommand("product")
	s.mu.Lock()
	for _, row := range s.rows {
		row["externalCode"] = cmd.CreateExternalCode
	}
	s.mu.Unlock()
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	writes, _, _ := s.state()
	if !errors.Is(err, ErrCatalogWriteUnknown) || r.State != catalog.ReceiptUnknown || writes != 0 {
		t.Fatalf("duplicate marker was selected or resent: %+v writes=%d error=%v", r, writes, err)
	}
}

func TestCatalogArchiveHasExplicitSelectedEffect(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c := s.writer(t)
	cmd := catalogUpdateCommand(catalogReadOne(t, c, "product", catalogTestProduct))
	cmd.Action, cmd.Values = catalog.ActionArchive, nil
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	_, bodies, _ := s.state()
	if err != nil || r.State != catalog.ReceiptApplied || !reflect.DeepEqual(bodies, []map[string]any{{"archived": true}}) {
		t.Fatalf("archive changed unrelated fields: %+v %+v %v", r, bodies, err)
	}
}
