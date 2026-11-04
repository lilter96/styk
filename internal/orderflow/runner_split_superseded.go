package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// A positive current parent and every recorded child are read before Begin.
// The final DB guard rederives membership/ownership and checks these clocks;
// these serial reads do not constitute an atomic remote snapshot.
func (r *Runner) supersededSplitPreflight(ctx context.Context, work orders.Work, parent orders.Posting, target *orders.TargetObservation) (orders.DispatchGuard, error) {
	if work.Command.SupersededSplitWorkID == "" {
		return orders.DispatchGuard{}, nil
	}
	if target == nil || !parent.Status.IsKnown() || *parent.Status.Value != "cancelled_from_split_pending" {
		return orders.DispatchGuard{}, orders.ErrStale
	}
	reviewer, ok := r.previews.(orders.Reviewer)
	if !ok {
		return orders.DispatchGuard{}, orders.ErrBlocked
	}
	review, err := reviewer.OrderReviewContext(ctx, work.Scope)
	if err != nil {
		return orders.DispatchGuard{}, err
	}
	facts, found := review.SplitSupersededReserves[parent.PostingNumber]
	if !found || facts.Split.ID != work.Command.SupersededSplitWorkID {
		return orders.DispatchGuard{}, orders.ErrStale
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return orders.DispatchGuard{}, err
	}
	children := make([]orders.Posting, 0, len(facts.Split.Receipt.PostingNumbers))
	for _, number := range facts.Split.Receipt.PostingNumbers {
		child, readErr := client.ReadOrderPosting(ctx, work.Scope, number)
		if readErr != nil {
			return orders.DispatchGuard{}, readErr
		}
		child.PII, child.Customer = nil, orders.ProtectedCustomer{}
		children = append(children, child)
	}
	parent.PII, parent.Customer = nil, orders.ProtectedCustomer{}
	return orders.DispatchGuard{SplitParentSource: &parent, SplitSupersededChildren: children, SplitSupersededTarget: target}, nil
}
