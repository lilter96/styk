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

func TestOwnedPositionCannotBecomeForeignMatchingNoop(t *testing.T) {
	t.Parallel()
	for _, changedIdentity := range []bool{true, false} {
		name := "remapped_product"
		if changedIdentity {
			name = "replaced_identity"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			command, target := scalarPositionRegression(t)
			prior := command.CustomerOrder.Positions[0]
			baseline := orders.PositionBaseline{Scope: command.Scope, WorkID: "native-origin", CommandID: "native-origin-command", Correlation: target.Correlation, ObservedAt: target.ObservedAt, Bindings: []orders.PositionBinding{{Source: prior, Native: target.Positions[0]}}}
			if err := baseline.Validate(); err != nil {
				t.Fatal(err)
			}
			wanted := prior
			wanted.Mapping.MoyskladID = "replacement-product"
			if changedIdentity {
				wanted.Identity.OfferID, wanted.Mapping.OfferID = "replacement-offer", "replacement-offer"
			}
			command.CustomerOrder.Positions = []orders.PositionWrite{wanted}
			foreign := target.Positions[0]
			foreign.ID, foreign.MoyskladID, foreign.Identity = "foreign-matching-values", wanted.Mapping.MoyskladID, wanted.Identity
			target.Positions = append(target.Positions, foreign)
			if validation := prepareMirrorUpdate(&command, target, baseline); validation.Code == "" {
				t.Fatal("foreign values were adopted despite a different owned UUID or mapping")
			}
			if command.PositionPatch != nil || command.PositionBaseline != nil {
				t.Fatal("foreign values created an owned mutation or scalar baseline")
			}
		})
	}
}

func TestMetadataOnlyUpdateCannotAdoptRecreatedPosition(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*orders.Command, *orders.TargetObservation)
		allow  bool
	}{
		{"name_only", func(*orders.Command, *orders.TargetObservation) {}, true},
		{"no_metadata", func(c *orders.Command, target *orders.TargetObservation) { target.Fields.Name = c.CustomerOrder.Name }, false},
		{"quantity", func(c *orders.Command, target *orders.TargetObservation) {
			c.CustomerOrder.Positions[0].Quantity, target.Positions[0].Quantity = "3", "3"
		}, false},
		{"mapping_revision", func(c *orders.Command, _ *orders.TargetObservation) { c.CustomerOrder.Positions[0].Mapping.Version++ }, false},
		{"reserve_missing", func(c *orders.Command, _ *orders.TargetObservation) {
			c.CustomerOrder.Positions[0].Reserve = orders.Field[orders.Decimal]{}
		}, false},
		{"application", func(c *orders.Command, target *orders.TargetObservation) {
			c.CustomerOrder.Applicable, target.Fields.Applicable = orders.Present(false), orders.Present(true)
		}, false},
		{"tax_flags", func(c *orders.Command, target *orders.TargetObservation) {
			c.CustomerOrder.VATEnabled, target.Fields.VATEnabled = orders.Present(false), orders.Present(true)
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command, target := scalarPositionRegression(t)
			baseline := orders.PositionBaseline{Scope: command.Scope, WorkID: "native-origin", CommandID: "native-origin-command", Correlation: target.Correlation, ObservedAt: target.ObservedAt, Bindings: []orders.PositionBinding{{Source: command.CustomerOrder.Positions[0], Native: target.Positions[0]}}}
			command.CustomerOrder.Name, target.Fields.Name = orders.Present("source-name"), orders.Present("manual-name")
			target.Fields.StateID = command.CustomerOrder.StateID
			target.Positions[0].ID = "recreated-position"
			test.change(&command, &target)
			validation := prepareMirrorUpdate(&command, target, baseline)
			assertScalarMetadataIntent(t, command, target, validation, test.allow)
		})
	}
}

func assertScalarMetadataIntent(t *testing.T, command orders.Command, target orders.TargetObservation, validation orders.Validation, allowed bool) {
	t.Helper()
	if (validation.Code == "") != allowed || command.PositionBaseline != nil || command.PositionPatch != nil {
		t.Fatal("metadata exception changed source inventory or adopted native UUID", validation)
	}
	if allowed && (command.Action != orders.ActionMirrorUpdate || !slices.Equal(command.CustomerOrder.OwnedFields, []string{"name"}) || !orders.NativePositionsEqual(command.TargetPositions, target.Positions)) {
		t.Fatal("metadata update lost its scalar-only frozen frontier")
	}
}
