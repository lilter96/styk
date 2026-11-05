package orderinventory

import (
	"math"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

// The ledger is gross fulfillment evidence, not a provider shipment counter.
// Fresh document and parent points plus a later stock traversal are mandatory.
func assessNativeConsumption(input Input, observation Observation, result Obligation, quantity int64) (Obligation, bool) {
	f := observation.NativeConsumption
	if f == nil {
		return result, false
	}
	if !f.Complete {
		return pending(result, Unknown, "native_consumption_coverage_unproven"), true
	}
	event, found := nativeConsumptionRepresentative(*f)
	if !found {
		return result, false
	}
	seal, issue := ordermoney.NativeConsumptionSnapshot(event.Work.Command, *f)
	if issue.Code != "" {
		return pending(result, Unknown, issue.Code), true
	}
	gross, sourceOK := nativeConsumptionPoolQuantity(input, observation, event.Work.Command, seal)
	if !sourceOK || f.Correlation != observation.Binding || observation.Target == nil || !orders.NativeParentReadback(event.Work.Command, *observation.Target) {
		return pending(result, Unknown, "native_unposted_source_or_parent_changed"), true
	}
	if !nativeConsumptionStockOrdered(input, observation) {
		return pending(result, ProjectionPending, "native_document_stock_recheck_required"), true
	}
	returned, code := nativeConsumptionPoints(input, observation, *f)
	if code != "" {
		return pending(result, Unknown, code), true
	}
	return classifyNativeConsumption(observation.Posting, result, quantity, gross, returned), true
}

func nativeConsumptionRepresentative(f orders.NativeConsumptionFacts) (orders.NativeConsumptionEvent, bool) {
	var selected orders.NativeConsumptionEvent
	found := false
	for _, event := range f.Events {
		m := event.Work.Command.NativeMovement
		if m == nil || m.ParentEffect != orders.NativeParentUnpostedDocument {
			continue
		}
		if !found || event.ConfirmedAt.After(selected.ConfirmedAt) {
			selected, found = event, true
		}
	}
	return selected, found
}

func classifyNativeConsumption(posting orders.Posting, result Obligation, quantity, gross int64, returned bool) Obligation {
	if gross > quantity {
		return pending(result, Unknown, "native_document_gross_exceeds_source_quantity")
	}
	closed := knownStatus(posting, "cancelled") || knownStatus(posting, "delivered")
	if returned && !closed {
		return pending(result, Unknown, "native_return_business_obligation_unverified")
	}
	if !knownStatus(posting, "cancelled") && gross != quantity {
		return pending(result, Unreflected, "native_document_obligation_not_fully_fulfilled")
	}
	coverage := int64(0) // The actual parent is explicitly unposted, not reserved.
	result.reserveCoverage, result.Reserve = &coverage, "0"
	result.State, result.Reason = Shipped, "native_document_gross_fulfillment_observed"
	if knownStatus(posting, "cancelled") {
		result.State, result.Reason = Cancelled, "native_document_source_closed"
	}
	return result
}

func nativeConsumptionPoolQuantity(input Input, o Observation, c orders.Command, seal orders.NativeConsumptionSeal) (int64, bool) {
	if c.Scope != o.Posting.Scope || c.CommercialOrderID != o.Posting.CommercialOrderID || c.PostingNumber != o.Posting.PostingNumber || c.Scope.Tenant != input.Pool.Tenant || c.Scope.MoyskladAccount != input.Pool.MoyskladAccount {
		return 0, false
	}
	quantities := make(map[string]string, len(seal.Positions))
	for _, position := range seal.Positions {
		quantities[position.OrderPositionID] = string(position.GrossDemand)
	}
	seen := make(map[string]bool, len(o.Lines))
	var total int64
	for _, source := range o.Posting.Lines {
		if !hasLine(o.Lines, source.Identity) {
			continue
		}
		binding, matched := nativeConsumptionSourceBinding(input, c, source)
		value, known := quantities[binding.Native.ID]
		gross, valid := whole(value)
		if !matched || !known || !valid || seen[source.Identity.Key()] || total > math.MaxInt64-gross {
			return 0, false
		}
		seen[source.Identity.Key()], total = true, total+gross
	}
	return total, len(seen) > 0 && len(seen) == len(o.Lines)
}

func nativeConsumptionSourceBinding(input Input, c orders.Command, source orders.PostingLine) (orders.PositionBinding, bool) {
	if source.ProductID.Validate() != nil {
		return orders.PositionBinding{}, false
	}
	for _, binding := range c.NativeMovement.SourceBaseline.Bindings {
		m := binding.Source.Mapping
		if binding.Source.Identity != source.Identity || m.Scope != c.Scope || m.MoyskladKind != input.Pool.ItemKind || m.MoyskladID != input.Pool.ItemID || !m.Matches(source.Identity) || !orders.ExactDecimalEqual(binding.Source.Quantity, source.Quantity) {
			continue
		}
		if source.ProductID.IsKnown() && *source.ProductID.Value != m.OzonProductID {
			return binding, false
		}
		return binding, true
	}
	return orders.PositionBinding{}, false
}
