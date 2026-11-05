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
	for i, ancestor := range proof.AncestorSources() {
		posting, e := source.ReadOrderPosting(ctx, w.Scope, ancestor.PostingNumber)
		if e != nil || !proof.AncestorSourceMatches(i, posting) {
			return checks, orders.ErrStale
		}
		posting.PII, posting.Customer = nil, orders.ProtectedCustomer{}
		checks.SplitAncestorSources = append(checks.SplitAncestorSources, posting)
	}
	return readSplitAncestorTargets(ctx, w, target, reader, checks)
}

func readSplitAncestorTargets(ctx context.Context, w orders.Work, target orders.Provider, reader orders.TargetReader, checks orders.DispatchGuard) (orders.DispatchGuard, error) {
	proof := w.Command.SplitAncestorSettlement
	for i, a := range proof.Ancestors {
		correlation, e := target.ReadOrderCorrelation(ctx, a.Target.Correlation)
		if e != nil || correlation != a.Target.Correlation {
			return checks, orders.ErrStale
		}
		native, e := reader.ReadCustomerOrder(ctx, correlation)
		if e != nil || !proof.AncestorTargetMatches(i, native) && !orders.SplitScalarMetadataAncestorTarget(w.Command, i, native) {
			return checks, orders.ErrStale
		}
		checks.SplitAncestorTargets = append(checks.SplitAncestorTargets, native)
	}
	return checks, nil
}
