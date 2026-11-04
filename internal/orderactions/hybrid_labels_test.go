package orderactions

import (
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestHybridDeliveringRequiresSeparatePrintAttestationAndLabelReference(t *testing.T) {
	t.Parallel()
	posting := rfbsPVZPosting(false)
	posting.AvailableActions = []string{"non_int_delivering"}
	proposal := orders.ActionProposal{PostingNumber: posting.PostingNumber, Action: orders.ActionPostingShip,
		Parameters: orders.ActionParameters{EventConfirmed: true, EventReference: "actual-handover", LabelPrintedConfirmed: true, LabelWorkID: "label-work"}}
	plan := PlanExecution(posting, *posting.DeliveryProfile, proposal.Action, rfbsTestNow)
	if !plan.Allowed || !slices.Contains(plan.ParametersRequired, "label_work_id") || !slices.Contains(plan.ParametersRequired, "label_printed_confirmed") {
		t.Fatal("hybrid label inputs hidden")
	}
	if got := ValidateExecution(posting, *posting.DeliveryProfile, proposal, nil, rfbsTestNow); len(got) != 0 {
		t.Fatalf("complete proposal blocked: %+v", got)
	}
	for _, mutate := range []func(*orders.ActionParameters){
		func(p *orders.ActionParameters) { p.LabelWorkID = "" },
		func(p *orders.ActionParameters) { p.LabelPrintedConfirmed = false },
		func(p *orders.ActionParameters) { p.EventConfirmed = false },
	} {
		changed := proposal
		mutate(&changed.Parameters)
		if len(ValidateExecution(posting, *posting.DeliveryProfile, changed, nil, rfbsTestNow)) == 0 {
			t.Fatal("missing actual prerequisite accepted")
		}
	}
}

func TestHybridLabelDoesNotAuthorizeCarrierOrLaterDeliveryTransitions(t *testing.T) {
	t.Parallel()
	posting := rfbsPVZPosting(false)
	posting.AvailableActions = nil
	posting.Status = orders.Present("delivering")
	if PlanExecution(posting, *posting.DeliveryProfile, orders.ActionPostingLastMile, rfbsTestNow).Allowed {
		t.Fatal("seller overrode Ozon pickup-point delivery")
	}
	posting = rfbsPVZPosting(true)
	if PlanExecution(posting, *posting.DeliveryProfile, orders.ActionPostingShip, rfbsTestNow).Allowed {
		t.Fatal("seller overrode integrated carrier")
	}
}
