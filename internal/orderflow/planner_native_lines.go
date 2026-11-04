package orderflow

import (
	"slices"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func planNativeValues(review orders.ReviewContext, posting orders.Posting, proposal orders.NativeMovementProposal, parent orders.TargetObservation, baseline orders.PositionBaseline, basis *orders.NativeDocumentObservation) (orders.NativeMovement, []orders.Validation) {
	fields := parent.Fields
	moment := proposal.EventOccurredAt
	if !*proposal.Applicable.Value {
		moment = proposal.PlannedMoment
	}
	movement := orders.NativeMovement{SourceBaseline: baseline, DemandWorkID: proposal.DemandWorkID, DemandBasis: basis, OrganizationID: fields.OrganizationID, AgentID: fields.AgentID, StoreID: fields.StoreID, CurrencyID: fields.CurrencyID, RateValue: fields.RateValue, Applicable: proposal.Applicable, VATEnabled: fields.VATEnabled, VATIncluded: fields.VATIncluded, Moment: orders.NativeAccountingMoment(moment), Lines: make([]orders.NativeMovementLine, 0, len(proposal.Lines))}
	if basis != nil {
		movement.VATEnabled, movement.VATIncluded = basis.VATEnabled, basis.VATIncluded
	}
	if proposal.Action == orders.ActionDemandCreate && *proposal.Applicable.Value && posting.Status.IsKnown() && *posting.Status.Value == "cancelled" {
		return movement, []orders.Validation{{Field: "posting.status", Code: "cancelled_posting_cannot_create_new_shipment"}}
	}
	if baseline.Scope != posting.Scope || baseline.Correlation != parent.Correlation || parent.Fields.StoreID != review.Config.StoreID || parent.Fields.OrganizationID != review.Config.OrganizationID || parent.Fields.AgentID != review.Config.AgentID {
		return movement, []orders.Validation{{Field: "customer_order", Code: "native_parent_scope_or_references_changed"}}
	}
	seen := make(map[string]bool, len(proposal.Lines))
	for _, selected := range proposal.Lines {
		if selected.Identity.Validate() != nil || selected.Quantity.Validate() != nil || seen[selected.Identity.Key()] {
			return movement, []orders.Validation{{Field: "lines", Code: "native_line_identity_or_quantity_invalid"}}
		}
		seen[selected.Identity.Key()] = true
		line, err := planNativeLine(review, posting, selected, parent, baseline, basis, *proposal.Applicable.Value)
		if err != nil {
			return movement, []orders.Validation{{Field: "positions", Code: "native_owned_line_or_remaining_quantity_unverified"}}
		}
		movement.Lines = append(movement.Lines, line)
	}
	return movement, nil
}

func planNativeLine(review orders.ReviewContext, posting orders.Posting, selected orders.ActionQuantity, parent orders.TargetObservation, baseline orders.PositionBaseline, basis *orders.NativeDocumentObservation, posted bool) (orders.NativeMovementLine, error) {
	binding, index, err := ownedNativeMovementLine(review, posting, selected, parent, baseline, basis)
	if err != nil {
		return orders.NativeMovementLine{}, err
	}
	current := parent.Positions[index]
	if !current.Price.IsKnown() || !current.Discount.IsKnown() || !current.VAT.IsKnown() || !current.VATEnabled.IsKnown() {
		return orders.NativeMovementLine{}, orders.ErrBlocked
	}
	line := orders.NativeMovementLine{Identity: selected.Identity, Mapping: binding.Source.Mapping, OrderPositionID: current.ID, Quantity: selected.Quantity, Price: *current.Price.Value, Discount: current.Discount, VAT: current.VAT, VATEnabled: current.VATEnabled}
	if basis != nil {
		return planNativeReturnLine(line, *basis)
	}
	if !current.Shipped.IsKnown() {
		return line, orders.ErrBlocked
	}
	if posted && (!current.Reserve.IsKnown() || !orders.ExactDecimalEqual(*current.Reserve.Value, "0") && !binding.Source.Reserve.IsKnown()) {
		return line, orders.ErrBlocked
	}
	available, err := ordermoney.RemainingQuantity(current.Quantity, *current.Shipped.Value)
	if err != nil {
		return line, err
	}
	if _, err = ordermoney.RemainingQuantity(available, selected.Quantity); err != nil {
		return line, err
	}
	return line, nil
}

func ownedNativeMovementLine(review orders.ReviewContext, posting orders.Posting, selected orders.ActionQuantity, parent orders.TargetObservation, baseline orders.PositionBaseline, basis *orders.NativeDocumentObservation) (orders.PositionBinding, int, error) {
	var binding orders.PositionBinding
	found := false
	for _, candidate := range baseline.Bindings {
		if candidate.Source.Identity.Key() == selected.Identity.Key() {
			binding, found = candidate, true
		}
	}
	if !found || !slices.ContainsFunc(review.ProductMappings, func(mapping orders.ProductMappingRef) bool {
		return mapping.ID == binding.Source.Mapping.ID && mapping.Version == binding.Source.Mapping.Version && mapping.Scope == posting.Scope && mapping.MoyskladID == binding.Source.Mapping.MoyskladID && mapping.MoyskladKind == binding.Source.Mapping.MoyskladKind
	}) {
		return binding, -1, orders.ErrStale
	}
	sourceFound := false
	projected, validations := ordermoney.ProjectLines(posting, review.ProductMappings)
	if len(validations) > 0 {
		return binding, -1, orders.ErrBlocked
	}
	for _, source := range projected {
		if source.Identity.Key() == selected.Identity.Key() {
			sourceFound = orders.ExactDecimalEqual(source.Quantity, binding.Source.Quantity) && source.Price.IsKnown() && binding.Source.Price.IsKnown() && source.Price.Value.Currency == binding.Source.Price.Value.Currency && orders.ExactDecimalEqual(source.Price.Value.Amount, binding.Source.Price.Value.Amount)
		}
	}
	if !sourceFound {
		return binding, -1, orders.ErrStale
	}
	for index, current := range parent.Positions {
		if current.ID != binding.Native.ID {
			continue
		}
		if !orders.PositionRepresented(binding.Source, current) || !nativeReserveProven(review, baseline, parent, basis, binding.Native, current) {
			return binding, -1, orders.ErrStale
		}
		return binding, index, nil
	}
	return binding, -1, orders.ErrBlocked
}

func planNativeReturnLine(line orders.NativeMovementLine, basis orders.NativeDocumentObservation) (orders.NativeMovementLine, error) {
	if !basis.ReturnsComplete || basis.Validate() != nil {
		return line, orders.ErrBlocked
	}
	index := -1
	for i, position := range basis.Positions {
		if position.ItemKind != line.Mapping.MoyskladKind || position.ItemID != line.Mapping.MoyskladID {
			continue
		}
		if index >= 0 {
			return line, orders.ErrConflict
		}
		index = i
	}
	if index < 0 {
		return line, orders.ErrBlocked
	}
	position := basis.Positions[index]
	consumed := make([]orders.Decimal, 0, len(basis.Returns)+1)
	for _, returned := range basis.Returns {
		if err := returned.Validate(basis.Correlation.DocumentID, position.Price.Currency); err != nil {
			return line, err
		}
		if !*returned.Applicable.Value {
			continue
		}
		for _, row := range returned.Positions {
			if row.ItemKind == position.ItemKind && row.ItemID == position.ItemID {
				consumed = append(consumed, row.Quantity)
			}
		}
	}
	consumed = append(consumed, line.Quantity)
	if _, err := ordermoney.RemainingQuantity(position.Quantity, consumed...); err != nil {
		return line, err
	}
	line.BasisPositionID, line.Price, line.Discount, line.VAT, line.VATEnabled = position.ID, position.Price, position.Discount, position.VAT, position.VATEnabled
	return line, nil
}

func nativeReserveProven(review orders.ReviewContext, baseline orders.PositionBaseline, parent orders.TargetObservation, basis *orders.NativeDocumentObservation, before, current orders.TargetPosition) bool {
	if orders.SameObservedDecimal(before.Reserve, current.Reserve) {
		return true
	}
	if basis == nil {
		return false
	}
	for _, known := range review.NativeDocuments {
		if known.Observation.Correlation != basis.Correlation || known.Observation.Scope != parent.Scope || known.Parent.Scope != parent.Scope || known.Parent.Correlation != parent.Correlation || !known.Parent.Complete {
			continue
		}
		if known.Parent.Correlation != baseline.Correlation || !orders.NativePositionsEqual(known.Parent.Positions, parent.Positions) {
			continue
		}
		return true
	}
	return false
}
