package orderinventory

import (
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
	"strconv"
	"strings"
	"testing"
	"time"
)

func nativeDemandFixture(t *testing.T, quantity int) Input {
	t.Helper()
	base := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	id := "00000000-0000-4000-8000-000000000001"
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: "source", OzonAccount: "seller"}
	identity := orders.LineIdentity{OfferID: "offer", SKU: "123"}
	correlation, correlationErr := orders.CorrelationFor(scope, "posting")
	if correlationErr != nil {
		t.Fatal(correlationErr)
	}
	correlation.CustomerOrderID = id
	mapping := orders.ProductMappingRef{ID: "1", Version: 1, Scope: scope, OzonProductID: "100", SKU: "123", OfferID: "offer", MoyskladKind: "product", MoyskladID: id, Confirmed: true, ConfirmedAt: base.Add(-time.Hour)}
	source := orders.PositionWrite{Identity: identity, Mapping: mapping, Quantity: "3", Reserve: orders.Present(orders.Decimal("3")), Price: orders.Present(orders.Money{Amount: "10", Currency: "RUB"}), VAT: orders.Present(orders.Decimal("0"))}
	native := orders.TargetPosition{ID: id, MoyskladKind: "product", MoyskladID: id, Quantity: "3", Reserve: source.Reserve, Shipped: orders.Present(orders.Decimal("0")), Price: source.Price, VAT: source.VAT, VATEnabled: orders.Present(false), Discount: orders.Present(orders.Decimal("0"))}
	foreign := native
	foreign.ID = "00000000-0000-4000-8000-000000000002"
	foreign.Quantity = "7"
	foreign.Reserve = orders.Present(orders.Decimal("2"))
	baseline := orders.PositionBaseline{Scope: scope, WorkID: "mirror-work", CommandID: "mirror-command", Correlation: correlation, ObservedAt: base.Add(-time.Minute), Bindings: []orders.PositionBinding{{Source: source, Native: native}}}
	movement := &orders.NativeMovement{SourceBaseline: baseline, OrganizationID: id, AgentID: id, StoreID: id, CurrencyID: id, RateValue: orders.Present(orders.Decimal("1")), Applicable: orders.Present(true), VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), Moment: base, Lines: []orders.NativeMovementLine{{Identity: identity, Mapping: mapping, OrderPositionID: id, Quantity: orders.Decimal(strconv.Itoa(quantity)), Price: *source.Price.Value, Discount: native.Discount, VAT: source.VAT, VATEnabled: native.VATEnabled}}, Event: orders.NativeMovementEvent{Kind: "shipment_dispatched", OccurredAt: base, Confirmed: true, Digest: strings.Repeat("a", 64), ProtectedInput: orders.ProtectedActionInput{Reference: strings.Repeat("b", 64), Revision: 1}}}
	var err error
	movement.Correlation, err = orders.NativeCorrelationFor(scope, orders.ActionDemandCreate, movement.Event.Digest)
	if err != nil {
		t.Fatal(err)
	}
	movement.Products = []orders.NativeProductRequirements{{Scope: scope, ItemKind: "product", ItemID: id, Archived: orders.Present(false), SerialTrackable: orders.Present(false), TrackingType: orders.Present("NOT_TRACKED"), Revision: "product-revision", ObservedAt: base}}
	command := orders.Command{ID: "native-command", Scope: scope, Action: orders.ActionDemandCreate, CommercialOrderID: "commercial", PostingNumber: "posting", Correlation: correlation, ConfigRevision: 1, Credentials: orders.CredentialRevisions{Moysklad: 1, Ozon: 1}, ExpectedSourceRevision: "revision", ExpectedTargetRevision: "before", SourceOrigin: orders.OriginOzon, NativeMovement: movement, TargetPositions: []orders.TargetPosition{native, foreign}, TargetPositionsComplete: true, Warehouse: orders.WarehouseMappingRef{ID: "1", Version: 1, Scope: scope, MoyskladStoreID: id, OzonWarehouseID: "warehouse", Confirmed: true, ConfirmedAt: base}, IdempotencyKey: "native-intent", CreatedAt: base}
	if err = command.Validate(); err != nil {
		t.Fatal("invalid native fixture", err)
	}
	native.Reserve = orders.Present(orders.Decimal(strconv.Itoa(3 - quantity)))
	native.Shipped = orders.Present(orders.Decimal(strconv.Itoa(quantity)))
	parent := orders.TargetObservation{Scope: scope, Correlation: correlation, ProviderUpdatedAt: orders.Present("2026-11-04 15:00:02.000"), Revision: "after", Complete: true, ObservedAt: base.Add(2 * time.Second), Fields: orders.CustomerOrderWrite{OrganizationID: id, AgentID: id, StoreID: id, CurrencyID: id}, Positions: []orders.TargetPosition{native, foreign}}
	document := orders.NativeDocumentObservation{Scope: scope, Correlation: movement.Correlation, OrganizationID: id, AgentID: id, StoreID: id, CurrencyID: id, RateValue: movement.RateValue, CustomerOrderID: orders.Present(id), Applicable: movement.Applicable, VATEnabled: movement.VATEnabled, VATIncluded: movement.VATIncluded, Moment: orders.Present(base), Sum: orders.Present(orders.Money{Amount: orders.Decimal(strconv.Itoa(quantity * 10)), Currency: "RUB"}), Revision: "document", Complete: true, ObservedAt: base.Add(2 * time.Second), Positions: []orders.NativeDocumentPosition{{ID: id, ItemKind: "product", ItemID: id, Quantity: movement.Lines[0].Quantity, Price: *source.Price.Value, Discount: native.Discount, VAT: native.VAT, VATEnabled: native.VATEnabled}}}
	document.Correlation.DocumentID = id
	posting := orders.Posting{Scope: scope, CommercialOrderID: "commercial", PostingNumber: "posting", Scheme: orders.Present("FBS"), WarehouseID: orders.Present("warehouse"), Status: orders.Present("awaiting_deliver"), Evidence: orders.EvidenceRevision{Origin: orders.OriginOzon, Revision: "revision", ObservedAt: base.Add(-time.Minute)}, Lines: []orders.PostingLine{{Identity: identity, ProductID: orders.Present("100"), Quantity: "3", CancelledQuantity: orders.Present(orders.Decimal("0"))}}}
	observation := Observation{Posting: posting, Binding: correlation, Lines: []orders.LineIdentity{identity}, Work: &orders.Work{ID: "native-work", Scope: scope, State: orders.StateReconciled, Command: command}, Target: &parent, NativeDocument: &document, NativeConfirmedAt: base.Add(3 * time.Second), NativeSyncID: ptr(correlation.SyncID), NativeStartedAt: base.Add(4 * time.Second), NativeFinishedAt: base.Add(5 * time.Second)}
	for _, position := range parent.Positions {
		observation.Positions = append(observation.Positions, domain.StockObservedOrderPosition{OrderID: id, PositionID: position.ID, ItemKind: "product", ItemID: id, WarehouseID: ptr(id), SourceUpdated: ptr("2026-11-04 15:00:02.000"), Quantity: ptr(string(position.Quantity)), Reserve: ptr(string(*position.Reserve.Value)), Shipped: ptr(string(*position.Shipped.Value)), Applicable: ptr(true), ObservedAt: base.Add(5 * time.Second)})
	}
	importID := int64(1)
	observed := base.Add(-30 * time.Second)
	pool := domain.StockPool{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, WarehouseID: id, ItemKind: "product", ItemID: id}
	stock := domain.StockSourceEvidence{Tenant: scope.Tenant, Account: scope.MoyskladAccount, WarehouseID: id, ItemKind: "product", ItemID: id, ImportID: &importID, ObservationFound: true, Physical: ptr(strconv.Itoa(10 - quantity)), Reserve: ptr(strconv.Itoa(5 - quantity)), ObservedAt: &observed, TraversalComplete: true, FreshTraversal: &domain.StockTraversalEvidence{OperationID: "DB-admitted-read", ImportID: importID, StartedAt: base.Add(6 * time.Second), FinishedAt: base.Add(7 * time.Second)}}
	return Input{Pool: pool, Baseline: stock, BaselineStartedAt: base.Add(-time.Hour), Now: base.Add(time.Minute), MaxAge: time.Hour, Observations: []Observation{observation}}
}

func TestNativeDemandFullShipmentDoesNotDeductForeignReserveTwice(t *testing.T) {
	t.Parallel()
	input := nativeDemandFixture(t, 3)
	guard := Assess(input)
	if guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != Shipped || guard.Obligations[0].Reserve != "0" {
		t.Fatalf("full demand proof did not settle: %+v", guard)
	}
	decision := inventoryDecision(input)
	if decision.Quantity == nil || *decision.Quantity != "5" {
		t.Fatalf("physical7/foreignreserve2 not deducted exactly once: %+v", decision)
	}
	baseline, err := orders.NativePositionBaseline(*input.Observations[0].Work, *input.Observations[0].Target, *input.Observations[0].NativeDocument, input.Observations[0].NativeConfirmedAt)
	if err != nil || baseline.EvidenceClass != orders.NativeDemandReadback || baseline.Bindings[0].Source.Reserve.Value == nil || *baseline.Bindings[0].Source.Reserve.Value != "0" {
		t.Fatal("native counter frontier not independently classified", err)
	}
	if baseline.Bindings[0].Source.Quantity != "3" || *input.Observations[0].Work.Command.NativeMovement.SourceBaseline.Bindings[0].Source.Reserve.Value != "3" {
		t.Fatal("original source ownership rewritten")
	}
}

func TestNativeDemandPartialUnknownAndStaleEvidenceRemainBlocked(t *testing.T) {
	t.Parallel()
	if guard := Assess(nativeDemandFixture(t, 1)); !guard.Blocked || guard.Obligations[0].State != Unreflected {
		t.Fatalf("partial demand was treated as settled: %+v", guard)
	}
	for _, change := range []func(*Input){
		func(i *Input) { i.Observations[0].Work.State = orders.StateUnknown },
		func(i *Input) { i.Observations[0].NativeDocument = nil },
		func(i *Input) { i.Baseline.FreshTraversal = nil; i.BaselineStartedAt = i.Now.Add(-time.Second) },
		func(i *Input) { i.Observations[0].Positions[0].SourceUpdated = ptr("2026-11-04 15:00:01.000") },
		func(i *Input) {
			i.Observations[0].NativeStartedAt = i.Observations[0].NativeConfirmedAt.Add(-time.Second)
		},
		func(i *Input) { i.Observations[0].Positions[1].Reserve = ptr("0") },
		func(i *Input) { i.Observations[0].Posting.Evidence.Revision = "changed" },
		func(i *Input) {
			i.Observations[0].Work.Command.NativeMovement.SourceBaseline.EvidenceClass = "OPERATOR_ATTESTED"
		},
		func(i *Input) { i.Observations[0].Work.Command.Action = orders.ActionSalesReturnCreate },
	} {
		input := nativeDemandFixture(t, 3)
		change(&input)
		if !Assess(input).Blocked {
			t.Fatal("incomplete or foreign native proof settled obligation")
		}
	}
}

func TestMirrorAttributionUsesFreshDatabaseTraversalOfUnchangedContent(t *testing.T) {
	t.Parallel()
	input := inventoryFixture()
	original := input.Now.Add(-5 * time.Minute)
	input.Baseline.ObservedAt = &original
	input.BaselineStartedAt = input.Now.Add(-6 * time.Minute)
	if !Assess(input).Blocked {
		t.Fatal("old snapshot alone included later native reserve")
	}
	input.Baseline.FreshTraversal = &domain.StockTraversalEvidence{OperationID: "shared-DB-read", ImportID: *input.Baseline.ImportID, StartedAt: input.Now.Add(-2 * time.Minute), FinishedAt: input.Now.Add(-time.Minute)}
	guard := Assess(input)
	if guard.Blocked || guard.Obligations[0].State != BaselineMatched {
		t.Fatalf("fresh unchanged source did not include native reserve %+v", guard)
	}
	decision := inventoryDecision(input)
	if decision.Quantity == nil || *decision.Quantity != "7" {
		t.Fatal("unchanged source content lost numeric candidate")
	}
	if !input.Baseline.ObservedAt.Equal(original) {
		t.Fatal("historical source timestamp rewritten")
	}
	input.Baseline.FreshTraversal.ImportID++
	if !Assess(input).Blocked {
		t.Fatal("unrelated traversal admitted old source snapshot")
	}
}
