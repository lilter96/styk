package orders

import "testing"

func TestReverseStatusMappingsRequireExplicitReviewedActions(t *testing.T) {
	t.Parallel()
	base := Config{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moysklad"}, Revision: 1}
	for _, test := range []struct {
		name    string
		mapping ReverseStatusMapping
		valid   bool
	}{
		{"pack", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingPack}, true},
		{"ship", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingShip}, true},
		{"deliver", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingDeliver}, true},
		{"cancel", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingCancel}, true},
		{"label", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingLabelCreate}, false},
		{"tracking", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingTracking}, false},
		{"mirror", ReverseStatusMapping{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionMirrorCreate}, false},
		{"bad-state", ReverseStatusMapping{StateID: "not-a-uuid", Action: ActionPostingPack}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			config.ReverseStatusMappings = []ReverseStatusMapping{test.mapping}
			err := config.validateReverseStatusMappings()
			if test.valid && err != nil || !test.valid && err == nil {
				t.Fatalf("err=%v", err)
			}
		})
	}
	config := base
	config.ReverseStatusMappings = []ReverseStatusMapping{
		{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingPack},
		{StateID: "11111111-1111-4111-8111-111111111111", Action: ActionPostingShip},
	}
	if err := config.validateReverseStatusMappings(); err != ErrInvalid {
		t.Fatalf("duplicate state accepted: %v", err)
	}
}

func TestReverseTransitionAllowedUsesReviewedPredecessors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		action Action
		status string
		want   bool
	}{
		{ActionPostingPack, "awaiting_packaging", true},
		{ActionPostingPack, "awaiting_deliver", false},
		{ActionPostingShip, "awaiting_deliver", true},
		{ActionPostingShip, "awaiting_packaging", false},
		{ActionPostingDeliver, "driver_pickup", true},
		{ActionPostingDeliver, "delivering", false},
		{ActionPostingCancel, "awaiting_packaging", true},
		{ActionPostingCancel, "awaiting_deliver", true},
		{ActionPostingCancel, "delivered", false},
		{ActionPostingTracking, "awaiting_deliver", false},
	} {
		if got := ReverseTransitionAllowed(test.action, test.status); got != test.want {
			t.Fatalf("%s from %s=%v", test.action, test.status, got)
		}
	}
}
