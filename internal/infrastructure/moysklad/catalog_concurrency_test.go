package moysklad

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func TestCatalogBindingAllowsOnlyOneConcurrentDispatch(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	c, cmd := s.writer(t), catalogCreateCommand("product")
	var authorized, applied, rejected atomic.Int32
	catalogBind(t, c, cmd, func(context.Context) (bool, error) { authorized.Add(1); return true, nil })
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			r, err := c.WriteCatalog(context.Background(), cmd)
			switch {
			case err == nil && r.State == catalog.ReceiptApplied:
				applied.Add(1)
			case errors.Is(err, ErrCatalogWritesDisabled) && r.State == catalog.ReceiptRejected:
				rejected.Add(1)
			default:
				t.Errorf("unexpected concurrent result: %+v %v", r, err)
			}
		})
	}
	wg.Wait()
	writes, _, _ := s.state()
	if authorized.Load() != 1 || applied.Load() != 1 || rejected.Load() != 1 || writes != 1 {
		t.Fatalf("binding was consumed more than once: authorized=%d applied=%d rejected=%d writes=%d", authorized.Load(), applied.Load(), rejected.Load(), writes)
	}
}

func TestCatalogVariantDescriptionClearPreservesParentAndCharacteristics(t *testing.T) {
	t.Parallel()
	s := newCatalogSimulator(t)
	s.add("variant", catalogTestVariant, "Parent (M)")
	c := s.writer(t)
	p := catalogReadOne(t, c, "variant", catalogTestVariant)
	before := s.unownedJSON(t, "variant", catalogTestVariant)
	cmd := catalogUpdateCommand(p)
	cmd.Values = map[catalog.Field]catalog.Value{catalog.FieldDescription: catalog.Clear()}
	catalogBind(t, c, cmd, catalogAuthorize)
	r, err := c.WriteCatalog(context.Background(), cmd)
	if err != nil || r.State != catalog.ReceiptApplied {
		t.Fatalf("variant clear failed: %+v %v", r, err)
	}
	after := catalogReadOne(t, c, "variant", catalogTestVariant)
	if after.Fields[catalog.FieldDescription].State != catalog.StateEmpty || after.Parent == nil || *after.Parent != *p.Parent || before != s.unownedJSON(t, "variant", catalogTestVariant) {
		t.Fatal("description clear changed the variant identity or unowned fields")
	}
}
