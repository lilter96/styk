package orderactions

import (
	"github.com/lilter96/styk/internal/orders"
	"testing"
)

func TestExpressDeliveryNeedsExactAdvertisedActionAndFactualEvent(t *testing.T) {
	t.Parallel()
	for _, express := range []orders.Field[bool]{orders.Present(true), {}} {
		for _, tc := range []struct {
			action        orders.Action
			status, token string
		}{{orders.ActionPostingShip, "awaiting_deliver", "non_int_delivering"}, {orders.ActionPostingLastMile, "delivering", "non_int_last_mile"}, {orders.ActionPostingDeliver, "driver_pickup", "non_int_delivered"}} {
			t.Run(string(express.Presence)+string(tc.action), func(t *testing.T) {
				t.Parallel()
				posting := rfbsTestPosting()
				posting.Status = orders.Present(tc.status)
				posting.DeliveryProfile.Express = express
				posting.Delivery.Express = express
				posting.AvailableActions = []string{tc.token}
				plan := PlanExecution(posting, *posting.DeliveryProfile, tc.action, rfbsTestNow)
				if !plan.Allowed {
					t.Fatalf("exact provider-advertised non-integrated action blocked: %+v", plan.Blockers)
				}
				proposal := orders.ActionProposal{PostingNumber: posting.PostingNumber, Action: tc.action, Parameters: orders.ActionParameters{EventConfirmed: true, EventReference: "actual-test-event"}}
				if got := ValidateExecution(posting, *posting.DeliveryProfile, proposal, nil, rfbsTestNow); len(got) != 0 {
					t.Fatalf("complete factual event rejected: %+v", got)
				}
				proposal.Parameters.EventConfirmed = false
				if len(ValidateExecution(posting, *posting.DeliveryProfile, proposal, nil, rfbsTestNow)) == 0 {
					t.Fatal("advertised action fabricated an actual delivery event")
				}
				for _, tokens := range [][]string{nil, {}, {"future_action"}} {
					posting.AvailableActions = tokens
					if PlanExecution(posting, *posting.DeliveryProfile, tc.action, rfbsTestNow).Allowed {
						t.Fatal("absent, empty or unknown actions authorized Express transition")
					}
				}
			})
		}
	}
}
