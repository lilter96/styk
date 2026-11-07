package orderflow

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestMerchantPlanningReadFailureIsNotAProvedStateChange(t *testing.T) {
	t.Parallel()
	review, intent := automaticNativeReview(t)
	intent.Action, intent.EventKind = orders.ActionPostingPack, "assembly_ready"
	review.Config.Lifecycle.Mappings[0].Action, review.Config.Lifecycle.Mappings[0].EventKind = intent.Action, intent.EventKind
	sealAutomaticNativeIntent(t, &intent)
	provider := &flowProvider{readErr: context.DeadlineExceeded}
	service := flowService(t, &flowStore{review: review}, flowResolver{target: provider})
	decision, err := service.planMerchantAction(context.Background(), orders.PreviewRequest{MerchantIntent: &intent}, review, review.Postings[0], orders.ActionProposal{PostingNumber: intent.Correlation.PostingNumber, Action: intent.Action}, true, intent.ObservedAt)
	if !errors.Is(err, context.DeadlineExceeded) || len(decision.Validation) != 0 || len(decision.Commands) != 0 || provider.writes != 0 {
		t.Fatal("transient read became a proved merchant change or command", decision, err)
	}
}
