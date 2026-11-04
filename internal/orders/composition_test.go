package orders

import (
	"slices"
	"testing"
	"time"
)

func compositionFixture(t *testing.T) (Command, PositionBaseline) {
	t.Helper()
	command, baseline := positionPatchFixture(t)
	old := &command.TargetPositions[0]
	old.VAT = Present(Decimal("0"))
	old.VATEnabled = Present(false)
	old.CompositionSafe = Present(true)
	command.CustomerOrder.Positions[0].VAT = old.VAT
	baseline.Bindings[0].Source = command.CustomerOrder.Positions[0]
	baseline.Bindings[0].Native = *old
	added := command.CustomerOrder.Positions[0]
	added.Identity = LineIdentity{OfferID: "second-offer", SKU: "second-sku"}
	added.Mapping.ID = "second-mapping"
	added.Mapping.MoyskladID = "second-native-product"
	added.Mapping.OzonProductID = "second-product"
	added.Mapping.OfferID = added.Identity.OfferID
	added.Mapping.SKU = added.Identity.SKU
	command.CustomerOrder.Positions = append(command.CustomerOrder.Positions, added)
	replacement, err := PlanPositionReplacement(command, baseline, Present(false))
	if err != nil {
		t.Fatal(err)
	}
	command.Action, command.PositionReplacement = ActionMirrorCompositionUpdate, replacement
	command.CustomerOrder.OwnedFields = []string{"positions.quantity", "positions.reserve", "positions.price", "positions.vat"}
	return command, baseline
}

func TestCompositionRequiresWholeOwnedUnshippedFrontier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*Command)
	}{
		{"valid", func(*Command) {}},
		{"no_consent", func(c *Command) { c.PositionReplacement.ConfirmReplacement = false }},
		{"foreign", func(c *Command) { c.TargetPositions = append(c.TargetPositions, TargetPosition{ID: "foreign"}) }},
		{"shipped", func(c *Command) { c.TargetPositions[0].Shipped = Present(Decimal("1")) }},
		{"unknown_shipped", func(c *Command) { c.TargetPositions[0].Shipped = Field[Decimal]{} }},
		{"unsupported_pack", func(c *Command) { c.TargetPositions[0].CompositionSafe = Present(false) }},
		{"missing_schema_proof", func(c *Command) { c.TargetPositions[0].CompositionSafe = Field[bool]{} }},
		{"operator_claim", func(c *Command) {
			c.PositionReplacement.Baseline.RecoveryID = "recovery"
			c.PositionReplacement.Baseline.EvidenceClass = "OPERATOR_ATTESTED"
			c.PositionReplacement.Baseline.ValidUntil = time.Now().Add(time.Hour)
		}},
		{"automation", func(c *Command) { c.AutomationGrantRevision = 1 }},
		{"missing_vat", func(c *Command) { c.CustomerOrder.Positions[1].VAT = Field[Decimal]{} }},
		{"wrong_scope", func(c *Command) { c.PositionReplacement.Baseline.Scope.Tenant = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command, _ := compositionFixture(t)
			tc.change(&command)
			err := command.Validate()
			if (err == nil) != (tc.name == "valid") {
				t.Fatalf("validate=%v", err)
			}
		})
	}
}

func compositionResult(command Command) TargetObservation {
	positions := slices.Clone(command.TargetPositions)
	added := command.CustomerOrder.Positions[1]
	positions = append(positions, TargetPosition{ID: "new-native-position", CompositionSafe: Present(true), MoyskladKind: added.Mapping.MoyskladKind, MoyskladID: added.Mapping.MoyskladID, Quantity: added.Quantity, Reserve: added.Reserve, Price: added.Price, VAT: added.VAT, VATEnabled: Present(false), Discount: Present(Decimal("0")), Shipped: Present(Decimal("0"))})
	return TargetObservation{Scope: command.Scope, Correlation: command.Correlation, Complete: true, Revision: "after", ObservedAt: time.Now(), Positions: positions}
}

func TestCompositionAcknowledgedReadbackLearnsOnlyExactNewUUIDs(t *testing.T) {
	t.Parallel()
	command, _ := compositionFixture(t)
	target := compositionResult(command)
	if !CompositionTargetObserved(command, target) {
		t.Fatal("complete exact result rejected")
	}
	work := Work{ID: "composition-work", Scope: command.Scope, State: StateReconciled, DispatchCount: 1, Command: command, Receipt: Receipt{State: StateAcknowledged}}
	baseline, err := ProjectionPositionBaseline(work, target)
	if err != nil || len(baseline.Bindings) != 2 {
		t.Fatalf("baseline=%+v err=%v", baseline, err)
	}
	work.Receipt.State = StateUnknown
	if _, err := ProjectionPositionBaseline(work, target); err == nil {
		t.Fatal("unknown collection write adopted matched SKU")
	}
	work.Receipt.State = StateAcknowledged
	work.DispatchCount = 0
	if _, err := ProjectionPositionBaseline(work, target); err == nil {
		t.Fatal("undispatched operation adopted new UUID")
	}
	target.Positions[0].ID = "manual-recreated-same-product"
	if CompositionTargetObserved(command, target) {
		t.Fatal("retained ID substitution accepted")
	}
}

func TestCompositionExactPreflightAndUnsupportedReadback(t *testing.T) {
	t.Parallel()
	command, _ := compositionFixture(t)
	target := TargetObservation{Scope: command.Scope, Correlation: command.Correlation, Complete: true, Revision: command.ExpectedTargetRevision, Positions: slices.Clone(command.TargetPositions), Fields: CustomerOrderWrite{VATEnabled: Present(false)}}
	if err := CompositionTargetCurrent(command, target); err != nil {
		t.Fatal(err)
	}
	target.Positions[0].CompositionSafe = Present(false)
	if err := CompositionTargetCurrent(command, target); err == nil {
		t.Fatal("new unsupported line field bypassed final preflight")
	}
	target = compositionResult(command)
	target.Positions[1].CompositionSafe = Present(false)
	if CompositionTargetObserved(command, target) {
		t.Fatal("unsupported result adopted")
	}
}
