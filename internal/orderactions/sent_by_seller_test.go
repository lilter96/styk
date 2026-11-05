package orderactions

import (
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestDeprecatedSentBySellerCannotAcquireExecutionPermission(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	posting.AvailableActions = []string{"sent_by_seller"}
	plan := PlanExecution(posting, *posting.DeliveryProfile, orders.ActionPostingSentBySeller, rfbsTestNow)
	deprecated := slices.ContainsFunc(plan.Blockers, func(v orders.Validation) bool { return v.Code == "provider_method_deprecated" })
	if plan.Allowed || !deprecated || slices.Contains(ExecutionActions(), orders.ActionPostingSentBySeller) {
		t.Fatal("deprecated action acquired dispatch permission or remained selectable")
	}
}
