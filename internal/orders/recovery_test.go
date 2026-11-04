package orders

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecoveryTargetExcludesPrivateFreeText(t *testing.T) {
	t.Parallel()
	target := TargetObservation{Fields: CustomerOrderWrite{Name: Present("PRIVATE_NAME"), Description: Present("PRIVATE_DESCRIPTION"), ShipmentAddress: Present("PRIVATE_ADDRESS"), Attributes: []AttributeWrite{{AttributeID: "attribute", Value: Present("PRIVATE_ATTRIBUTE")}}}, Complete: true, Revision: "hash", ObservedAt: time.Now()}
	evidence := NewRecoveryTarget(target)
	raw, err := json.Marshal(evidence)
	if err != nil || strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("native private fields persisted as recovery evidence")
	}
}

func TestUnknownCreatesAndFulfillmentCannotUseNativeUpdateRecovery(t *testing.T) {
	t.Parallel()
	command := testCommand(t)
	command.Correlation.CustomerOrderID = "00000000-0000-4000-8000-000000000001"
	command.ExpectedTargetRevision = "native-version"
	work := Work{Scope: command.Scope, State: StateUnknown, DispatchCount: 1, Command: command}
	for _, action := range []Action{ActionMirrorCreate, ActionPostingSplit, ActionPostingLabelCreate, ActionPostingCancel, ActionPostingShip} {
		work.Command.Action = action
		if work.RecoveryUpdateEligible() {
			t.Fatalf("uncertain %s became a recoverable native update", action)
		}
	}
	for _, action := range []Action{ActionMirrorUpdate} {
		work.Command.Action = action
		if !work.RecoveryUpdateEligible() {
			t.Fatalf("known native %s blocked", action)
		}
	}
	work.Command.Correlation.CustomerOrderID = ""
	if work.RecoveryUpdateEligible() {
		t.Fatal("unknown external identity was eligible")
	}
}

func TestRecoveryEligibilityRequiresValidScopedNativePatch(t *testing.T) {
	t.Parallel()
	command, baseline := positionPatchFixture(t)
	command.Correlation.CustomerOrderID = "00000000-0000-4000-8000-000000000001"
	baseline.Correlation = command.Correlation
	command.CustomerOrder.Positions[0].Quantity = "3"
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil {
		t.Fatal(err)
	}
	command.Action, command.PositionPatch = ActionMirrorPositionUpdate, patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	work := Work{Scope: command.Scope, State: StateUnknown, DispatchCount: 1, Command: command}
	if !work.RecoveryUpdateEligible() {
		t.Fatal("known valid native patch blocked")
	}
	work.Command.Scope.Tenant = "other"
	if work.RecoveryUpdateEligible() {
		t.Fatal("cross-tenant patch was recoverable")
	}
}

func TestRecoveryFieldsPreserveHistoricalBaselineSerialization(t *testing.T) {
	t.Parallel()
	_, baseline := positionPatchFixture(t)
	raw, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"recovery_id", "evidence_class", "valid_until"} {
		if strings.Contains(string(raw), name) {
			t.Fatalf("zero-valued %s changed historical command fingerprint", name)
		}
	}
	var decoded PositionBaseline
	if err = json.Unmarshal(raw, &decoded); err != nil || !baseline.Equal(decoded) {
		t.Fatal("historical position baseline roundtrip changed")
	}
}
