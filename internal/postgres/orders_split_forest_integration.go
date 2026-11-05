package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func loadLateOwnedAncestorSettlement(ctx context.Context, q ordersQuerier, nested orders.NestedSplitSettlementFacts, number, selfID, fingerprint string) (orders.SplitAncestorSettlementFacts, error) {
	facts, err := loadDeferredOwnedSettlement(ctx, q, nested, number, selfID, fingerprint)
	if err == nil || !historicalSplitUnavailable(err) {
		return facts, err
	}
	return loadOwnedForestSettlement(ctx, q, nested, number, selfID, fingerprint)
}

func forestOwnedMappingsCurrent(f orders.SplitForestFacts, current *orders.ReviewContext) bool {
	for _, native := range f.Native {
		if native.Work.Command.CustomerOrder == nil || !retainedAdmissionMappingsCurrent(orders.RetainedSplitAdmissionFacts{Admission: native.Work}, current) {
			return false
		}
	}
	return true
}
