package orders

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func testScope() Scope {
	return Scope{Tenant: "tenant-a", OzonAccount: "ozon-a", MoyskladAccount: "moy-a"}
}

func testPosting() Posting {
	return Posting{Scope: testScope(), CommercialOrderID: "commercial-500", PostingNumber: "posting-500-1",
		Status: Present("awaiting_packaging"), Evidence: EvidenceRevision{Revision: "facts-1", ObservedAt: time.Unix(100, 0).UTC(), Origin: OriginOzon},
		Lines: []PostingLine{{Identity: LineIdentity{SKU: "sku-100", OfferID: "offer-100"}, Quantity: "5"},
			{Identity: LineIdentity{SKU: "sku-200", OfferID: "offer-200"}, Quantity: "2"}}}
}

func testCommand(t *testing.T) Command {
	t.Helper()
	scope, now := testScope(), time.Unix(100, 0).UTC()
	correlation, err := CorrelationFor(scope, "posting-500-1")
	if err != nil {
		t.Fatal(err)
	}
	mapping := ProductMappingRef{ID: "mapping-1", Version: 1, Scope: scope, OzonProductID: "product-300",
		SKU: "sku-100", OfferID: "offer-100", MoyskladKind: "product", MoyskladID: "moy-item", Confirmed: true, ConfirmedAt: now}
	warehouse := WarehouseMappingRef{ID: "warehouse-1", Version: 1, Scope: scope, OzonWarehouseID: "ozon-warehouse",
		MoyskladStoreID: "moy-store", Confirmed: true, ConfirmedAt: now}
	write := CustomerOrderWrite{OrganizationID: "org", AgentID: "agent", StoreID: "moy-store", StateID: "state", SalesChannelID: "channel",
		Positions: []PositionWrite{{Identity: LineIdentity{SKU: "sku-100", OfferID: "offer-100"}, Mapping: mapping, Quantity: "5"}}}
	return Command{ID: "command-1", Scope: scope, Action: ActionMirrorCreate, CommercialOrderID: "commercial-500", PostingNumber: "posting-500-1",
		Correlation: correlation, ConfigRevision: 1, Credentials: CredentialRevisions{Ozon: 1, Moysklad: 1}, ExpectedSourceRevision: "facts-1",
		SourceOrigin: OriginOzon, Warehouse: warehouse, CustomerOrder: &write, IdempotencyKey: "intent-1", CreatedAt: now}
}

func TestDuplicateLineIdentityNeedsProviderLineID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		lines     []LineIdentity
		ambiguous bool
	}{
		{"duplicate SKU and offer", []LineIdentity{{SKU: "sku", OfferID: "offer"}, {SKU: "sku", OfferID: "offer"}}, true},
		{"one missing line ID", []LineIdentity{{SKU: "sku", OfferID: "offer", ProviderLineID: "one"}, {SKU: "sku", OfferID: "offer"}}, true},
		{"distinct provider lines", []LineIdentity{{SKU: "sku", OfferID: "offer", ProviderLineID: "one"}, {SKU: "sku", OfferID: "offer", ProviderLineID: "two"}}, false},
		{"reused provider line", []LineIdentity{{SKU: "one", ProviderLineID: "line"}, {SKU: "two", ProviderLineID: "line"}}, true},
		{"distinct offers", []LineIdentity{{SKU: "sku", OfferID: "one"}, {SKU: "sku", OfferID: "two"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for range 2 {
				err := ValidateLineIdentities(tc.lines)
				if errors.Is(err, ErrAmbiguousLine) != tc.ambiguous || (err != nil && !tc.ambiguous) {
					t.Fatalf("identity validation = %v", err)
				}
				slices.Reverse(tc.lines)
			}
		})
	}
}

func TestSplitPostingCorrelationsStayIsolated(t *testing.T) {
	t.Parallel()
	first, err := CorrelationFor(testScope(), "posting-child-1")
	if err != nil {
		t.Fatal(err)
	}
	otherScope := testScope()
	otherScope.OzonAccount = "other-ozon"
	for _, input := range []struct {
		scope   Scope
		posting string
	}{{testScope(), "posting-child-2"}, {otherScope, "posting-child-1"}} {
		other, err := CorrelationFor(input.scope, input.posting)
		if err != nil {
			t.Fatal(err)
		}
		if first.SyncID == other.SyncID || first.Marker == other.Marker {
			t.Fatal("split children or account boundaries shared a correlation")
		}
	}
	if strings.Contains(first.Marker, testScope().Tenant) || first.Validate() != nil {
		t.Fatal("correlation must be opaque and scoped")
	}
}

func TestPostingFactsIgnoreReceptionAndArrayOrder(t *testing.T) {
	t.Parallel()
	first := testPosting()
	first.Lines[0].CancelledQuantity = Present(Decimal("2"))
	second := first
	second.Lines = slices.Clone(first.Lines)
	slices.Reverse(second.Lines)
	second.Evidence.ObservedAt = time.Unix(200, 0).UTC()
	second.Evidence.CheckedAt = time.Unix(201, 0).UTC()
	second.Evidence.LocalSequence = 10
	second.PII = &Customer{Name: Present("private-name")}
	a, err := PostingFingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PostingFingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || first.Lines[0].Quantity != "5" || *first.Lines[0].CancelledQuantity.Value != "2" {
		t.Fatal("fact comparison changed partial-cancellation quantities or depended on reception/order")
	}
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-name") {
		t.Fatal("PII leaked into normal posting JSON")
	}
}

func TestMirrorRequiresConfirmedScopedMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Command)
	}{
		{"unconfirmed", func(c *Command) { c.CustomerOrder.Positions[0].Mapping.Confirmed = false }},
		{"wrong account", func(c *Command) { c.CustomerOrder.Positions[0].Mapping.Scope.OzonAccount = "other" }},
		{"SKU used as product ID", func(c *Command) { c.CustomerOrder.Positions[0].Mapping.OzonProductID = "" }},
		{"wrong SKU", func(c *Command) { c.CustomerOrder.Positions[0].Mapping.SKU = "other-sku" }},
		{"STYK echo", func(c *Command) { c.SourceOrigin = OriginSTYK }},
		{"another store", func(c *Command) { c.CustomerOrder.StoreID = "other-store" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := testCommand(t)
			if err := command.Validate(); err != nil {
				t.Fatal(err)
			}
			tc.change(&command)
			if err := command.Validate(); err == nil {
				t.Fatal("invalid mapping or provenance allowed a mirror write")
			}
		})
	}
}

func TestUncertainWorkCannotDispatchAgain(t *testing.T) {
	t.Parallel()
	for _, state := range []State{StateAcknowledged, StatePolling, StateUnknown, StateReconciled, StateRejected, StateBlocked} {
		for _, next := range []State{StateReady, StateQueued, StateClaimed, StateDispatching} {
			if CanTransition(state, next) {
				t.Fatalf("unsafe redispatch transition %s -> %s", state, next)
			}
		}
	}
}

func TestTargetSerializationMasksNativeFreeTextWithoutMutatingFacts(t *testing.T) {
	target := TargetObservation{Fields: CustomerOrderWrite{Name: Present("Synthetic private native name"), Description: Present("Synthetic private native description"), Attributes: []AttributeWrite{{AttributeID: "private", Value: Present("Synthetic private attribute")}}}, Revision: "native-fingerprint"}
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Synthetic private") {
		t.Fatal("unmanaged free text leaked into durable target")
	}
	if !strings.Contains(string(data), "redacted_fields") || target.Fields.Description.Value == nil || *target.Fields.Description.Value != "Synthetic private native description" {
		t.Fatal("privacy masking changed observed facts")
	}
}
