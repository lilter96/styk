package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) splitAncestorPreflight(ctx context.Context, w orders.Work, target orders.Provider, checks orders.DispatchGuard) (orders.DispatchGuard, error) {
	proof := w.Command.SplitAncestorSettlement
	if proof == nil {
		return checks, nil
	}
	source, err := resolveOrderClient(ctx, r.resolver, w.Scope, "ozon", w.Credentials.Ozon)
	if err != nil {
		return checks, err
	}
	reader, ok := target.(orders.TargetReader)
	if !ok {
		return checks, orders.ErrBlocked
	}
	for i, a := range proof.Ancestors {
		p, e := source.ReadOrderPosting(ctx, w.Scope, a.Posting.PostingNumber)
		if e != nil || !a.SourceMatches(p) {
			return checks, orders.ErrStale
		}
		correlation, e := target.ReadOrderCorrelation(ctx, a.Target.Correlation)
		if e != nil || correlation != a.Target.Correlation {
			return checks, orders.ErrStale
		}
		native, e := reader.ReadCustomerOrder(ctx, correlation)
		if e != nil || !proof.AncestorTargetMatches(i, native) && !orders.SplitScalarMetadataAncestorTarget(w.Command, i, native) {
			return checks, orders.ErrStale
		}
		p.PII, p.Customer = nil, orders.ProtectedCustomer{}
		checks.SplitAncestorSources = append(checks.SplitAncestorSources, p)
		checks.SplitAncestorTargets = append(checks.SplitAncestorTargets, native)
	}
	return checks, nil
}
