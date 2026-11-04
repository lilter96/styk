package orders

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func applicationFixture(t *testing.T) (Command, PositionBaseline, TargetObservation) {
	t.Helper()
	command, baseline := positionPatchFixture(t)
	command.CustomerOrder.Applicable = Present(false)
	command.CustomerOrder.OwnedFields = []string{"applicable"}
	target := TargetObservation{Scope: command.Scope, Correlation: command.Correlation, Revision: command.ExpectedTargetRevision, Complete: true, ObservedAt: time.Now().UTC(), Fields: CustomerOrderWrite{Applicable: Present(true)}, Positions: slices.Clone(command.TargetPositions)}
	return command, baseline, target
}

func TestApplicationFlipRequiresCompleteImmutableOwnership(t *testing.T) {
	t.Parallel()
	command, baseline, target := applicationFixture(t)
	if command.Validate() == nil {
		t.Fatal("old queued aggregate flip without proof accepted")
	}
	proof, err := PlanApplicationBaseline(command, target, baseline)
	if err != nil {
		t.Fatal(err)
	}
	command.PositionBaseline = proof
	if err = command.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = ApplicationTargetCurrent(command, target); err != nil {
		t.Fatal(err)
	}
	if ApplicationTargetObserved(command, target) {
		t.Fatal("before-write state marked reconciled")
	}
	target.Fields.Applicable = Present(false)
	if !ApplicationTargetObserved(command, target) {
		t.Fatal("fully owned observed application flip rejected")
	}
	command.PositionBaseline = nil
	if ApplicationTargetObserved(command, target) {
		t.Fatal("UNKNOWN old intent falsely reconciled without ownership proof")
	}
}

func TestApplicationFlipBlocksForeignUnknownStaleAndAttestedFrontiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Command, *PositionBaseline, *TargetObservation)
	}{
		{"foreign reserve other SKU", func(c *Command, _ *PositionBaseline, o *TargetObservation) {
			foreign := c.TargetPositions[0]
			foreign.ID = "foreign-position"
			foreign.MoyskladID = "foreign-product"
			foreign.Reserve = Present(Decimal("2"))
			c.TargetPositions = append(c.TargetPositions, foreign)
			o.Positions = append(o.Positions, foreign)
		}},
		{"unknown shipped", func(c *Command, b *PositionBaseline, o *TargetObservation) {
			c.TargetPositions[0].Shipped = Field[Decimal]{}
			b.Bindings[0].Native.Shipped = Field[Decimal]{}
			o.Positions[0].Shipped = Field[Decimal]{}
		}},
		{"unknown application", func(_ *Command, _ *PositionBaseline, o *TargetObservation) { o.Fields.Applicable = Field[bool]{} }},
		{"foreign scope", func(_ *Command, b *PositionBaseline, _ *TargetObservation) { b.Scope.Tenant = "other" }},
		{"changed native UUID", func(c *Command, _ *PositionBaseline, o *TargetObservation) {
			c.TargetPositions[0].ID = "new-position"
			o.Positions[0].ID = "new-position"
		}},
		{"operator attestation", func(_ *Command, b *PositionBaseline, _ *TargetObservation) { b.EvidenceClass = "OPERATOR_ATTESTED" }},
		{"non-owned positive reserve", func(c *Command, b *PositionBaseline, o *TargetObservation) {
			c.CustomerOrder.Positions[0].Reserve = Field[Decimal]{}
			b.Bindings[0].Source.Reserve = Field[Decimal]{}
			b.Bindings[0].Native.Reserve = Present(Decimal("1"))
			c.TargetPositions[0].Reserve = Present(Decimal("1"))
			o.Positions[0].Reserve = Present(Decimal("1"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c, b, o := applicationFixture(t)
			test.edit(&c, &b, &o)
			if _, err := PlanApplicationBaseline(c, o, b); err == nil {
				t.Fatal("unsafe aggregate application flip planned")
			}
		})
	}
	command, baseline, target := applicationFixture(t)
	proof, err := PlanApplicationBaseline(command, target, baseline)
	if err != nil {
		t.Fatal(err)
	}
	command.PositionBaseline = proof
	target.Revision = "changed"
	if ApplicationTargetCurrent(command, target) == nil {
		t.Fatal("stale target revision accepted")
	}
}

func TestApplicationGuardDoesNotChangeHistoricalHashesOrUnrelatedActions(t *testing.T) {
	t.Parallel()
	command, _, _ := applicationFixture(t)
	command.CustomerOrder.OwnedFields = []string{"name"}
	if err := command.Validate(); err != nil {
		t.Fatal("ordinary scalar update newly blocked", err)
	}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("position_baseline")) {
		t.Fatal("absent application proof changed historical JSON")
	}
	var decoded Command
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(decoded)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("historical command bytes changed", err)
	}
	command.Action = ActionMirrorCreate
	command.Correlation.CustomerOrderID = ""
	if command.ValidateApplicationBaseline() != nil {
		t.Fatal("initial application setting required existing ownership")
	}
}

func TestScalarUpdateNeverAdoptsMatchingRecreatedUUID(t *testing.T) {
	t.Parallel()
	command, baseline, target := applicationFixture(t)
	command.CustomerOrder.OwnedFields = []string{"name"}
	work := Work{ID: "scalar-work", Scope: command.Scope, State: StateReconciled, Command: command}
	if _, err := ProjectionPositionBaseline(work, target); err == nil {
		t.Fatal("scalar without original identity proof created ownership")
	}
	carried, err := LegacyScalarPositionBaseline(work, target, baseline)
	if err != nil || !carried.Equal(baseline) {
		t.Fatal("legacy unchanged frontier lost original proof", err)
	}
	target.Positions[0].ID = "manually-recreated-position"
	if _, err = LegacyScalarPositionBaseline(work, target, baseline); err == nil {
		t.Fatal("matching assortment/quantity adopted a new UUID")
	}
	work.Command.PositionBaseline = &baseline
	work.Command.TargetPositions[0].ID = "manually-recreated-position"
	if _, err = ProjectionPositionBaseline(work, target); err == nil {
		t.Fatal("scalar carried ownership onto recreated UUID")
	}
}

func TestApplicationLegacyReadonlyFactsStayUnknownButCurrentFactsAreRequired(t *testing.T) {
	t.Parallel()
	command, baseline, target := applicationFixture(t)
	baseline.Bindings[0].Native.Shipped = Field[Decimal]{}
	baseline.Bindings[0].Native.Discount = Field[Decimal]{}
	prior, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := PlanApplicationBaseline(command, target, baseline)
	if err != nil {
		t.Fatal("later observed read-only zero was treated as a manual identity change", err)
	}
	if !proof.Bindings[0].Native.Shipped.IsMissing() {
		t.Fatal("historical missing counter rewritten as zero")
	}
	after, err := json.Marshal(baseline)
	if err != nil || !bytes.Equal(prior, after) {
		t.Fatal("ownership evidence history changed", err)
	}
	command.TargetPositions[0].Shipped = Field[Decimal]{}
	target.Positions[0].Shipped = Field[Decimal]{}
	if _, err = PlanApplicationBaseline(command, target, baseline); err == nil {
		t.Fatal("unknown current shipped count permitted aggregate change")
	}
}

func TestPositionPatchRetainsInheritedOperatorScopeAndExpiry(t *testing.T) {
	t.Parallel()
	command, baseline := positionPatchFixture(t)
	baseline.EvidenceClass = "OPERATOR_ATTESTED"
	baseline.RecoveryID = "recovery-attestation"
	baseline.ValidUntil = baseline.ObservedAt.Add(time.Hour)
	command.CustomerOrder.Positions[0].Quantity = "3"
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil || patch == nil {
		t.Fatal("patch fixture", err)
	}
	command.Action = ActionMirrorPositionUpdate
	command.PositionPatch = patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	target := TargetObservation{Scope: command.Scope, Correlation: command.Correlation, Revision: "after", Complete: true, ObservedAt: baseline.ObservedAt.Add(time.Minute), Positions: slices.Clone(command.TargetPositions)}
	target.Positions[0].Quantity = "3"
	result, err := ProjectionPositionBaseline(Work{ID: "patch-work", Scope: command.Scope, State: StateReconciled, Command: command, DispatchCount: 1}, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.EvidenceClass != "OPERATOR_ATTESTED" || result.RecoveryID != baseline.RecoveryID || !result.ValidUntil.Equal(baseline.ValidUntil) || !result.ObservedAt.Equal(baseline.ObservedAt) {
		t.Fatal("successful PUT promoted or extended an operator assumption")
	}
}
