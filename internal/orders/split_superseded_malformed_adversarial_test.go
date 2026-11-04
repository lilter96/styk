package orders

import "testing"

func TestSupersededSplitMalformedFieldsAreRejectedWithoutPanicking(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*SplitSupersededReserveFacts)
	}{
		{"missing warehouse value", func(f *SplitSupersededReserveFacts) { f.Before.WarehouseID = Field[string]{Presence: PresenceValue} }},
		{"missing applicable value", func(f *SplitSupersededReserveFacts) {
			f.Target.Fields.Applicable = Field[bool]{Presence: PresenceValue}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer supersededNoPanic(t)
			facts, _ := supersededSplitFixture(t)
			tc.edit(&facts)
			if _, err := NewSupersededSplitReserve(facts); err == nil {
				t.Fatal("malformed source/target field accepted")
			}
		})
	}
}

func TestSupersededSplitMalformedPlanFieldIsRejectedWithoutPanicking(t *testing.T) {
	t.Parallel()
	defer supersededNoPanic(t)
	facts, command := supersededSplitFixture(t)
	permission, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	command.CustomerOrder.Applicable = Field[bool]{Presence: PresenceValue}
	if _, err = PlanSupersededSplitReserve(command, permission); err == nil {
		t.Fatal("malformed command application field accepted")
	}
}

func supersededNoPanic(t *testing.T) {
	t.Helper()
	if value := recover(); value != nil {
		t.Fatalf("domain validation panicked instead of rejecting malformed fields: %v", value)
	}
}
