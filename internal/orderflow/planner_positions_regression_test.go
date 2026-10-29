package orderflow

import (
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func scalarPositionRegression(t *testing.T) (orders.Command, orders.TargetObservation) {
	t.Helper()
	review := flowReview(t, "posting")
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{}), review)
	command := preview.Decisions[0].Commands[0]
	target := flowTarget(command)
	command.Action, command.Correlation = orders.ActionMirrorUpdate, target.Correlation
	command.ExpectedTargetRevision = target.Revision
	command.CustomerOrder.OwnedFields = []string{"state"}
	command.TargetPositions, command.TargetPositionsComplete = slices.Clone(target.Positions), true
	return command, target
}

func TestTargetRepresentsScalarFieldsCannotHideChangedPositionValues(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*orders.PositionWrite)
	}{
		{"quantity", func(position *orders.PositionWrite) { position.Quantity = "3" }},
		{"reserve", func(position *orders.PositionWrite) { position.Reserve = orders.Present(orders.Decimal("1")) }},
		{"price", func(position *orders.PositionWrite) {
			position.Price = orders.Present(orders.Money{Amount: "99", Currency: "RUB"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command, target := scalarPositionRegression(t)
			test.change(&command.CustomerOrder.Positions[0])
			if targetRepresents(command, target) {
				t.Fatal("scalar field ownership hid an unapplied source position change")
			}
			target.Fields.StateID = "previous-state"
			if validation := prepareMirrorUpdate(&command, target); validation.Code == "" {
				t.Fatal("scalar change bypassed position replacement review")
			}
		})
	}
}

func TestTargetRepresentsPreservesUnchangedUnownedExtraPositions(t *testing.T) {
	t.Parallel()
	command, target := scalarPositionRegression(t)
	for index := range target.Positions {
		target.Positions[index].Managed = false
		target.Positions[index].Identity = orders.LineIdentity{}
	}
	target.Positions = append(target.Positions, orders.TargetPosition{ID: "manual-position", MoyskladKind: "product", MoyskladID: "manual-product", Quantity: "7", Reserve: orders.Present(orders.Decimal("2"))})
	command.TargetPositions = slices.Clone(target.Positions)
	if !targetRepresents(command, target) {
		t.Fatal("unchanged manual positions prevented a legitimate scalar-only readback")
	}
	target.Positions[1].Reserve = orders.Present(orders.Decimal("3"))
	if targetRepresents(command, target) {
		t.Fatal("changed unowned reservation was accepted as unchanged")
	}
}
