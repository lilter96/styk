package main

import "testing"

func TestProbeEligibilityRequiresOwnRegistrationAndAccount(t *testing.T) {
	for _, tc := range []struct {
		registered                   bool
		configured, selected, secret string
		want                         bool
	}{
		{true, "source-a", "source-a", "fixture", true},
		{false, "source-a", "source-a", "fixture", false},
		{true, "source-a", "source-b", "fixture", false},
		{true, "source-a", "source-a", "", false},
		{true, "", "", "fixture", false},
	} {
		if got := probeEligible(tc.registered, tc.configured, tc.selected, tc.secret); got != tc.want {
			t.Fatalf("probe eligibility: got %t want %t", got, tc.want)
		}
	}
}
