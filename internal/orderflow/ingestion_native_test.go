package orderflow

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type nativeIngestionStore struct {
	*ingestionTestStore
	candidates orders.NativePointCandidates
	cursors    []string
}

func (s *nativeIngestionStore) NativeOrderCandidates(_ context.Context, _ orders.Scope, _ int, cursor string) (orders.NativePointCandidates, error) {
	s.cursors = append(s.cursors, cursor)
	return s.candidates, nil
}

func nativeIngestionFixture(t *testing.T) (*Ingestor, *nativeIngestionStore, *flowProvider) {
	t.Helper()
	base, store, _ := newIngestionFixture(t)
	correlation, err := orders.CorrelationFor(store.config.Scope, "posting-1")
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = "native-order"
	provider := &flowProvider{correlation: correlation, target: orders.TargetObservation{Scope: correlation.Scope, Correlation: correlation, Revision: "native-revision", Complete: true, ObservedAt: base.options.Now(), Fields: orders.CustomerOrderWrite{StoreID: "store", Applicable: orders.Present(false)}, Positions: []orders.TargetPosition{{ID: "position-1", MoyskladKind: "product", MoyskladID: "product-1", Quantity: "2", Reserve: orders.Present(orders.Decimal("0")), Shipped: orders.Present(orders.Decimal("0"))}}}}
	nativeStore := &nativeIngestionStore{ingestionTestStore: store, candidates: orders.NativePointCandidates{Correlations: []orders.Correlation{correlation}}}
	base.store = nativeStore
	base.options.NativeResolver = flowResolver{target: provider}
	return base, nativeStore, provider
}

func TestNativeIngestionKeepsExactEvidenceSeparateAndResumes(t *testing.T) {
	t.Parallel()
	ingestor, store, provider := nativeIngestionFixture(t)
	store.candidates.NextCursor = "posting-1"
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, orders.NativePointStream); err != nil {
		t.Fatal(err)
	}
	page := store.pages[0]
	if page.Complete || page.Exhausted || len(page.Postings) != 0 || len(page.Secrets) != 0 || len(page.NativeReads) != 1 || page.NativeReads[0].Point == nil || provider.writes != 0 {
		t.Fatal("native read fabricated provider-wide or mutation evidence")
	}
	point := page.NativeReads[0].Point
	if !point.Positions[0].Reserve.IsKnown() || *point.Positions[0].Reserve.Value != "0" {
		t.Fatal("explicit reserve zero lost")
	}
	store.candidates = orders.NativePointCandidates{}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, orders.NativePointStream); err != nil {
		t.Fatal(err)
	}
	if store.cursors[1] != "posting-1" || !store.pages[1].Complete || !store.pages[1].Exhausted || provider.reads != 1 || provider.lookups != 1 {
		t.Fatal("native cursor did not resume without repeated provider reads")
	}
}

func TestNativeIngestionRejectsForeignCandidateBeforeNetwork(t *testing.T) {
	t.Parallel()
	ingestor, store, provider := nativeIngestionFixture(t)
	store.candidates.Correlations[0].Scope.Tenant = "other"
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, orders.NativePointStream); !errors.Is(err, orders.ErrInvalid) {
		t.Fatalf("foreign candidate error: %v", err)
	}
	if provider.reads != 0 || provider.lookups != 0 || len(store.pages) != 0 {
		t.Fatal("foreign candidate reached provider or checkpoint")
	}
}

func TestNativeIngestionPersistsFailureWithoutInventingZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"missing", orders.ErrNotFound, "NATIVE_ORDER_NOT_FOUND"},
		{"schema", orders.ErrSchemaDrift, "NATIVE_ORDER_SCHEMA_DRIFT"},
		{"interrupted", context.DeadlineExceeded, "NATIVE_READ_INTERRUPTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ingestor, store, provider := nativeIngestionFixture(t)
			provider.readErr = tc.err
			if err := ingestor.CollectStream(context.Background(), store.config.Scope, orders.NativePointStream); err != nil {
				t.Fatal(err)
			}
			page := store.pages[0]
			if page.Complete || len(page.NativeReads) != 1 || page.NativeReads[0].Point != nil || page.NativeReads[0].Code != tc.code || len(page.Failures) != 1 || provider.writes != 0 {
				t.Fatal("failed read was erased or represented as stock zero")
			}
		})
	}
}
