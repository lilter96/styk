package orders

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func positionPatchFixture(t *testing.T) (Command, PositionBaseline) {
	t.Helper()
	command := testCommand(t)
	command.CustomerOrder.Positions[0].Reserve = Present(Decimal("0"))
	command.CustomerOrder.Positions[0].Price = Present(Money{Amount: "12.34", Currency: "RUB"})
	correlation := command.Correlation
	correlation.CustomerOrderID = "native-order"
	source := command.CustomerOrder.Positions[0]
	native := TargetPosition{ID: "native-position", MoyskladKind: source.Mapping.MoyskladKind, MoyskladID: source.Mapping.MoyskladID, Quantity: source.Quantity, Reserve: source.Reserve, Price: source.Price, Shipped: Present(Decimal("0")), Discount: Present(Decimal("0"))}
	target := TargetObservation{Scope: command.Scope, Correlation: correlation, Positions: []TargetPosition{native}, ObservedAt: time.Now().UTC(), Complete: true}
	baseline, err := ProjectionPositionBaseline(Work{ID: "prior-work", DispatchCount: 1, Scope: command.Scope, State: StateReconciled, Command: command}, target)
	if err != nil {
		t.Fatal(err)
	}
	command.Action = ActionMirrorUpdate
	command.Correlation = correlation
	command.ExpectedTargetRevision = "target-version"
	command.TargetPositions = slices.Clone(target.Positions)
	command.TargetPositionsComplete = true
	return command, baseline
}

func TestPositionPatchUsesExactBaselineAndPreservesIndependentLines(t *testing.T) {
	t.Parallel()
	command, baseline := positionPatchFixture(t)
	command.CustomerOrder.Positions[0].Quantity = "3"
	command.TargetPositions = append(command.TargetPositions, TargetPosition{ID: "foreign", MoyskladKind: "product", MoyskladID: "foreign-product", Quantity: "7", Reserve: Present(Decimal("2"))})
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil || patch == nil || patch.PositionID != "native-position" || len(patch.Fields()) != 1 {
		t.Fatalf("patch=%+v err=%v", patch, err)
	}
	command.Action, command.PositionPatch = ActionMirrorPositionUpdate, patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	if err = command.Validate(); err != nil {
		t.Fatal(err)
	}
	target := TargetObservation{Positions: slices.Clone(command.TargetPositions)}
	target.Positions[0].Quantity = "3.000"
	if !PositionPatchObserved(command, target) {
		t.Fatal("exact position update was not observed")
	}
	target.Positions[1].Reserve = Present(Decimal("1"))
	if PositionPatchObserved(command, target) {
		t.Fatal("foreign reservation was released")
	}
}

func TestPositionPatchBlocksUnprovenOrStructuralChanges(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*Command, *PositionBaseline)
	}{
		{"wrong account", func(c *Command, b *PositionBaseline) { b.Scope.MoyskladAccount = "other" }},
		{"changed native UUID", func(c *Command, b *PositionBaseline) { c.TargetPositions[0].ID = "other" }},
		{"changed native reserve", func(c *Command, b *PositionBaseline) { c.TargetPositions[0].Reserve = Present(Decimal("2")) }},
		{"shipped", func(c *Command, b *PositionBaseline) { c.TargetPositions[0].Shipped = Present(Decimal("1")) }},
		{"unknown shipped", func(c *Command, b *PositionBaseline) { c.TargetPositions[0].Shipped = Field[Decimal]{} }},
		{"changed mapping", func(c *Command, b *PositionBaseline) { c.CustomerOrder.Positions[0].Mapping.Version++ }},
		{"removed source line", func(c *Command, b *PositionBaseline) { c.CustomerOrder.Positions = nil }},
		{"new source identity", func(c *Command, b *PositionBaseline) { c.CustomerOrder.Positions[0].Identity.OfferID = "other" }},
		{"unknown reserve ownership", func(c *Command, b *PositionBaseline) {
			b.Bindings[0].Source.Reserve = Field[Decimal]{}
			c.CustomerOrder.Positions[0].Reserve = Present(Decimal("1"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c, b := positionPatchFixture(t)
			c.CustomerOrder.Positions[0].Quantity = "3"
			test.change(&c, &b)
			if _, err := PlanPositionPatch(c, b); err == nil {
				t.Fatal("unproven patch allowed")
			}
		})
	}
}

func TestPositionPricePatchRefusesDiscountedOrUnownedPrice(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*Command, *PositionBaseline)
	}{
		{"discounted", func(c *Command, b *PositionBaseline) { c.TargetPositions[0].Discount = Present(Decimal("5")) }},
		{"discount unknown", func(c *Command, b *PositionBaseline) { c.TargetPositions[0].Discount = Field[Decimal]{} }},
		{"currency mismatch", func(c *Command, b *PositionBaseline) {
			c.CustomerOrder.Positions[0].Price = Present(Money{Amount: "99", Currency: "USD"})
		}},
		{"price not owned", func(c *Command, b *PositionBaseline) { b.Bindings[0].Source.Price = Field[Money]{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c, b := positionPatchFixture(t)
			c.CustomerOrder.Positions[0].Price = Present(Money{Amount: "99", Currency: "RUB"})
			test.change(&c, &b)
			if _, err := PlanPositionPatch(c, b); err == nil {
				t.Fatal("unverified price patch allowed")
			}
		})
	}
}

func TestPositionPatchValidationSurvivesDurableJSONRoundTrip(t *testing.T) {
	t.Parallel()
	command, baseline := positionPatchFixture(t)
	command.CustomerOrder.Positions[0].Quantity = "3"
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil {
		t.Fatal(err)
	}
	command.Action, command.PositionPatch = ActionMirrorPositionUpdate, patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var restored Command
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err = restored.Validate(); err != nil {
		t.Fatalf("durable patch invalidated by canonical missing field encoding: %v", err)
	}
}

func TestNativePositionKeepsDocumentedNegativeDiscountAsSurcharge(t *testing.T) {
	t.Parallel()
	command, _ := positionPatchFixture(t)
	position := command.TargetPositions[0]
	position.Discount = Present(Decimal("-10"))
	if !NativePositionEqual(position, position) {
		t.Fatal("documented surcharge was rejected as invalid native evidence")
	}
	position.Discount = Present(Decimal("10"))
	if NativePositionEqual(command.TargetPositions[0], position) {
		t.Fatal("changed native discount was ignored")
	}
}
