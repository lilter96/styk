package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) splitParentPreflight(ctx context.Context, work orders.Work, target orders.Provider) (orders.DispatchGuard, error) {
	proof := work.Command.SplitTransfer
	if proof == nil {
		return orders.DispatchGuard{}, nil
	}
	source, err := resolveOrderClient(ctx, r.resolver, work.Scope, "ozon", work.Credentials.Ozon)
	if err != nil {
		return orders.DispatchGuard{}, err
	}
	parent, err := source.ReadOrderPosting(ctx, work.Scope, proof.ParentPosting.PostingNumber)
	if err != nil || !proof.ParentSourceMatches(parent) {
		return orders.DispatchGuard{}, orders.ErrStale
	}
	reader, ok := target.(orders.TargetReader)
	if !ok {
		return orders.DispatchGuard{}, orders.ErrBlocked
	}
	correlation, err := target.ReadOrderCorrelation(ctx, proof.ParentCorrelation)
	if err != nil || correlation != proof.ParentCorrelation {
		return orders.DispatchGuard{}, orders.ErrStale
	}
	native, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil || !proof.ParentTargetMatches(native) {
		return orders.DispatchGuard{}, orders.ErrStale
	}
	parent.PII, parent.Customer = nil, orders.ProtectedCustomer{}
	return orders.DispatchGuard{SplitParentSource: &parent, SplitParentTarget: &native}, nil
}
