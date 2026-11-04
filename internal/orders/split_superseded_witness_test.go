package orders

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSupersededSplitPermissionMustBeReconstructedAtUse(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitFixture(t)
	permission, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(permission)
	if err != nil {
		t.Fatal(err)
	}
	var untrusted SplitSupersededReservePermission
	if err = json.Unmarshal(raw, &untrusted); err != nil {
		t.Fatal(err)
	}
	if _, err = PlanSupersededSplitReserve(command, untrusted); err == nil {
		t.Fatal("serialized caller facts became authority without scoped reconstruction")
	}
	if _, err = PlanSupersededSplitReserve(command, SplitSupersededReservePermission{}); err == nil {
		t.Fatal("literal permission became authority")
	}
	rebuilt, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PlanSupersededSplitReserve(command, rebuilt); err != nil {
		t.Fatal("fresh scoped reconstruction blocked", err)
	}
}

func TestSupersededSplitPermissionDropsUnneededPrivateData(t *testing.T) {
	t.Parallel()
	facts, _ := supersededSplitFixture(t)
	const private = "private-customer-field"
	facts.Target.Fields.Name, facts.Target.Fields.Description = Present(private), Present(private)
	facts.Target.Fields.ShipmentAddress = Present(private)
	facts.Parent.PII = &Customer{Name: Present(private)}
	facts.Parent.Delivery.TrackingNumber = Present(private)
	facts.Parent.Analytics.City = Present(private)
	facts.Parent.Lines[0].Name = Present(private)
	permission, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(permission)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), private) {
		t.Fatal("reserve permission retained private data unrelated to owned native reserve")
	}
}

func TestSupersededSplitRejectsChangedRetiredParentAllocation(t *testing.T) {
	t.Parallel()
	facts, _ := supersededSplitFixture(t)
	facts.Parent.Lines[0].Quantity = "1"
	if _, err := NewSupersededSplitReserve(facts); err == nil {
		t.Fatal("changed parent allocation released original owned reserve")
	}
}
