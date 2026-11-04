package orders

import (
	"strings"
	"testing"
	"time"
)

func nativeDomainFixture(t *testing.T) (Command, NativeDocumentPolicy) {
	t.Helper()
	command, baseline := positionPatchFixture(t)
	command.Correlation.CustomerOrderID = "00000000-0000-4000-8000-000000000010"
	baseline.Correlation = command.Correlation
	baseline.Bindings[0].Native.ID = "00000000-0000-4000-8000-000000000011"
	ref := "00000000-0000-4000-8000-000000000012"
	command.Warehouse.MoyskladStoreID = ref
	movement := &NativeMovement{Correlation: NativeDocumentCorrelation{Kind: "demand", SyncID: command.Correlation.SyncID, Marker: "styk-native-test"}, SourceBaseline: baseline, OrganizationID: ref, AgentID: ref, StoreID: ref, CurrencyID: ref, RateValue: Present(Decimal("1")), Applicable: Present(false), VATEnabled: Present(false), VATIncluded: Present(false), Moment: time.Now().UTC().Truncate(time.Minute), Event: NativeMovementEvent{Kind: "draft_plan", OccurredAt: time.Now().UTC(), Digest: strings.Repeat("a", 64), ProtectedInput: ProtectedActionInput{Reference: strings.Repeat("b", 64), Revision: command.ConfigRevision}}}
	correlation, err := NativeCorrelationFor(command.Scope, ActionDemandCreate, movement.Event.Digest)
	if err != nil {
		t.Fatal(err)
	}
	movement.Correlation = correlation
	source := baseline.Bindings[0].Source
	source.Mapping.MoyskladID = "00000000-0000-4000-8000-000000000014"
	baseline.Bindings[0].Source = source
	baseline.Bindings[0].Native.MoyskladID = source.Mapping.MoyskladID
	movement.SourceBaseline = baseline
	movement.Products = []NativeProductRequirements{{Scope: command.Scope, ItemKind: "product", ItemID: source.Mapping.MoyskladID, Archived: Present(false), SerialTrackable: Present(false), TrackingType: Present("NOT_TRACKED"), Revision: "product-revision", ObservedAt: time.Now().UTC()}}
	movement.Lines = []NativeMovementLine{{Identity: source.Identity, Mapping: source.Mapping, OrderPositionID: baseline.Bindings[0].Native.ID, Quantity: "2", Price: *source.Price.Value, Discount: Present(Decimal("0")), VAT: Present(Decimal("0")), VATEnabled: Present(false)}}
	command.Action, command.NativeMovement = ActionDemandCreate, movement
	command.CustomerOrder = nil
	native := baseline.Bindings[0].Native
	native.Price = Present(movement.Lines[0].Price)
	native.Discount = movement.Lines[0].Discount
	native.VAT = movement.Lines[0].VAT
	native.VATEnabled = movement.Lines[0].VATEnabled
	command.TargetPositions = []TargetPosition{native}
	command.TargetPositionsComplete = true
	policy := NativeDocumentPolicy{Enabled: true, Actions: []Action{ActionDemandCreate, ActionSalesReturnCreate}, MaximumQuantity: "10", EventMaxAgeSeconds: 3600}
	return command, policy
}

func TestNativeDraftRequiresExplicitValuesAndNoPhysicalAssertion(t *testing.T) {
	t.Parallel()
	command, policy := nativeDomainFixture(t)
	if err := command.NativeMovement.Validate(command); err != nil {
		t.Fatal(err)
	}
	if !command.NativeMovement.AllowedBy(&policy, command.Action, time.Now()) {
		t.Fatal("explicit native draft policy rejected")
	}
	for _, change := range []func(*NativeMovement){
		func(m *NativeMovement) { m.Applicable = Field[bool]{} },
		func(m *NativeMovement) { m.VATEnabled = Field[bool]{} },
		func(m *NativeMovement) { m.Lines[0].VAT = Field[Decimal]{} },
		func(m *NativeMovement) { m.SourceBaseline.Correlation.Scope.Tenant = "other" },
	} {
		c, _ := nativeDomainFixture(t)
		change(c.NativeMovement)
		if c.NativeMovement.Validate(c) == nil {
			t.Fatal("missing fact became an implicit false/zero or foreign basis was accepted")
		}
	}
}

func TestNativePostingNeedsUnexpiredPolicyAndPhysicalEvent(t *testing.T) {
	t.Parallel()
	command, policy := nativeDomainFixture(t)
	m := command.NativeMovement
	m.Applicable = Present(true)
	if m.Validate(command) == nil {
		t.Fatal("draft planning implied physical dispatch")
	}
	m.Event.Kind, m.Event.Confirmed = "shipment_dispatched", true
	if m.Validate(command) != nil {
		t.Fatal("explicit physical-event attestation rejected")
	}
	if m.AllowedBy(&policy, command.Action, time.Now()) {
		t.Fatal("posted document bypassed the separate policy")
	}
	now := time.Now().UTC()
	policy.AllowPosted, policy.AttestedAt, policy.ValidUntil = true, now.Add(-time.Minute), now.Add(time.Hour)
	if !m.AllowedBy(&policy, command.Action, now) {
		t.Fatal("current explicit native policy rejected")
	}
	if m.AllowedBy(&policy, command.Action, now.Add(2*time.Hour)) {
		t.Fatal("expired native policy authorized a stock movement")
	}
	m.RateValue = Field[Decimal]{}
	if m.Validate(command) == nil {
		t.Fatal("unknown exchange rate became an invented unit rate")
	}
}
