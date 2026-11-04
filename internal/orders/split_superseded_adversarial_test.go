package orders

import (
	"slices"
	"testing"
)

func TestSupersededSplitAdversarialRejectsDeletedOrDriftingProviderFacts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*SplitSupersededReserveFacts)
	}{
		{"deleted native document", func(f *SplitSupersededReserveFacts) { f.Target.SourceDeleted = Present("2026-11-04 19:00:00.000") }},
		{"malformed native deletion evidence", func(f *SplitSupersededReserveFacts) { f.Target.SourceDeleted = Field[string]{Presence: PresenceValue} }},
		{"parent assembly failed", func(f *SplitSupersededReserveFacts) { f.Parent.Substatus = Present("ship_failed") }},
		{"parent status drift", func(f *SplitSupersededReserveFacts) {
			f.Parent.Drift = []Drift{{Path: "$.result.status", Code: "wrong_type"}}
		}},
		{"parent chronology conflict", func(f *SplitSupersededReserveFacts) {
			f.Parent.Drift = []Drift{{Path: "evidence", Code: "ORDERING_PROVIDER_REVISION_CONFLICT"}}
		}},
		{"current child quantity drift", func(f *SplitSupersededReserveFacts) {
			f.Children = slices.Clone(f.Children)
			f.Children[0].Drift = []Drift{{Path: "$.result.products[0].quantity", Code: "wrong_type"}}
		}},
		{"original source quantity drift", func(f *SplitSupersededReserveFacts) {
			f.Before.Drift = []Drift{{Path: "$.result.products[0].quantity", Code: "wrong_type"}}
		}},
		{"acknowledged child quantity drift", func(f *SplitSupersededReserveFacts) {
			f.Split.Receipt.ObservedPostings = slices.Clone(f.Split.Receipt.ObservedPostings)
			f.Split.Receipt.ObservedPostings[0].Drift = []Drift{{Path: "$.result.products[0].quantity", Code: "wrong_type"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, _ := supersededSplitFixture(t)
			tc.edit(&facts)
			if _, err := NewSupersededSplitReserve(facts); err == nil {
				t.Fatal("untrustworthy source/native facts authorized releasing owned reserves")
			}
		})
	}
}

func TestSupersededSplitAdversarialRevalidatesPermissionBeforePlanning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*SplitSupersededReservePermission)
	}{
		{"missing split work identity", func(p *SplitSupersededReservePermission) { p.SplitWorkID = "" }},
		{"ordinary cancelled parent", func(p *SplitSupersededReservePermission) { p.ParentPosting.Status = Present("cancelled") }},
		{"parent no longer retired", func(p *SplitSupersededReservePermission) { p.ParentPosting.Status = Present("awaiting_packaging") }},
		{"missing original source", func(p *SplitSupersededReservePermission) { p.Before = Posting{} }},
		{"missing conserving child frontier", func(p *SplitSupersededReservePermission) { p.ChildPostings = nil }},
		{"child quantity changed after proof", func(p *SplitSupersededReservePermission) {
			p.ChildPostings[0].Lines[0].Quantity = "1"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, command := supersededSplitFixture(t)
			permission, err := NewSupersededSplitReserve(facts)
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(&permission)
			if _, err = PlanSupersededSplitReserve(command, permission); err == nil {
				t.Fatal("mutated exported permission authorized a native reserve-zero patch")
			}
		})
	}
}

func TestSupersededSplitAdversarialKeepsInformationalWarningsDistinct(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitFixture(t)
	facts.Before.Drift = []Drift{{Path: "$", Code: "schema_unverified"}}
	facts.Parent.Drift = []Drift{{Path: "$", Code: "schema_unverified"}, {Path: "$.result.future_optional", Code: "unknown_key"}}
	for index := range facts.Children {
		facts.Children[index].Drift = []Drift{{Path: "$", Code: "schema_unverified"}}
	}
	permission, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal("informational schema coverage warning became blanket reserve blocker", err)
	}
	plan, err := PlanSupersededSplitReserve(command, permission)
	if err != nil || plan.PositionPatch == nil {
		t.Fatal("otherwise exact owned reserve release was lost", err)
	}
}
