package orders

import "time"

func (movement NativeMovement) validateHeader(command Command) error {
	if !command.Action.IsNativeMovement() || command.CustomerOrder != nil || command.PostingAction != nil || command.PositionPatch != nil || command.SourceOrigin != OriginOzon {
		return ErrInvalid
	}
	if movement.Correlation.Validate() != nil || movement.Correlation.Kind != command.Action.NativeDocumentKind() || movement.Correlation.DocumentID != "" || command.Correlation.CustomerOrderID == "" || !validID(command.ExpectedTargetRevision) {
		return ErrInvalid
	}
	expected, err := NativeCorrelationFor(command.Scope, command.Action, movement.Event.Digest)
	if err != nil || expected != movement.Correlation {
		return ErrStale
	}
	if movement.SourceBaseline.Validate() != nil || movement.SourceBaseline.Scope != command.Scope || movement.SourceBaseline.Correlation != command.Correlation {
		return ErrStale
	}
	for _, id := range []string{movement.OrganizationID, movement.AgentID, movement.StoreID, movement.CurrencyID} {
		if !validUUID(id) {
			return ErrInvalid
		}
	}
	if movement.StoreID != command.Warehouse.MoyskladStoreID || movement.Moment.IsZero() || !knownNativeBoolean(movement.Applicable) || !knownNativeBoolean(movement.VATEnabled) || !knownNativeBoolean(movement.VATIncluded) {
		return ErrInvalid
	}
	if movement.RateValue.Validate() != nil || movement.RateValue.IsKnown() && !positiveDecimal(*movement.RateValue.Value) {
		return ErrInvalid
	}
	if err := movement.Event.Validate(command.ConfigRevision, *movement.Applicable.Value); err != nil {
		return err
	}
	if *movement.Applicable.Value && (!movement.RateValue.IsKnown() || !nativeEventKindMatches(command.Action, movement.Event.Kind)) {
		return ErrBlocked
	}
	return movement.validateParentEffect(command)
}

func (movement NativeMovement) Validate(command Command) error {
	if err := movement.validateHeader(command); err != nil {
		return err
	}
	if movement.Retry != nil && movement.Retry.Validate(command) != nil {
		return ErrInvalid
	}
	if len(movement.Lines) == 0 || len(movement.Lines) > 100 {
		return ErrInvalid
	}
	if err := movement.validateLines(command.Scope); err != nil {
		return err
	}
	if err := movement.validateOwnedLines(command); err != nil {
		return err
	}
	if err := movement.validateProducts(command.Scope, command.Action); err != nil {
		return err
	}
	return movement.validateBasis(command.Action)
}

func knownNativeBoolean(field Field[bool]) bool {
	return field.Validate() == nil && field.IsKnown()
}

func nativeEventKindMatches(action Action, kind string) bool {
	return action == ActionDemandCreate && kind == "shipment_dispatched" || action == ActionSalesReturnCreate && kind == "return_received"
}

func (movement NativeMovement) validateLines(scope Scope) error {
	seen := make(map[string]bool, len(movement.Lines))
	for _, line := range movement.Lines {
		if line.Identity.Validate() != nil || line.Mapping.Validate() != nil || line.Mapping.Scope != scope || !line.Mapping.Matches(line.Identity) || !validUUID(line.OrderPositionID) || seen[line.OrderPositionID] || !positiveDecimal(line.Quantity) || line.Price.Validate() != nil || !nonnegativeDecimal(line.Price.Amount) {
			return ErrInvalid
		}
		if line.Discount.Validate() != nil || !line.Discount.IsKnown() || line.Discount.Value.Validate() != nil || line.VAT.Validate() != nil || !line.VAT.IsKnown() || !nonnegativeDecimal(*line.VAT.Value) || !knownNativeBoolean(line.VATEnabled) {
			return ErrInvalid
		}
		// Native totals currently have a verified simple monetary subset. Block
		// unsupported rounding before dispatch, not after a physical movement.
		if !ExactDecimalEqual(*line.Discount.Value, "0") || !NativeTaxPreservesPrice(line, movement.VATEnabled, movement.VATIncluded) {
			return ErrBlocked
		}
		if line.BasisPositionID != "" && !validUUID(line.BasisPositionID) {
			return ErrInvalid
		}
		seen[line.OrderPositionID] = true
	}
	return nil
}

func (movement NativeMovement) validateBasis(action Action) error {
	if action == ActionDemandCreate {
		if movement.DemandWorkID != "" || movement.DemandBasis != nil {
			return ErrInvalid
		}
		return nil
	}
	basis := movement.DemandBasis
	if !validID(movement.DemandWorkID) || basis == nil || basis.Validate() != nil || !nativeDocumentPresent(basis.SourceDeleted) || basis.Scope != movement.SourceBaseline.Scope || basis.Correlation.Kind != "demand" || !basis.ReturnsComplete || !basis.Applicable.IsKnown() || !*basis.Applicable.Value {
		return ErrBlocked
	}
	if !basis.CustomerOrderID.IsKnown() || *basis.CustomerOrderID.Value != movement.SourceBaseline.Correlation.CustomerOrderID || basis.OrganizationID != movement.OrganizationID || basis.AgentID != movement.AgentID || basis.CurrencyID != movement.CurrencyID {
		return ErrStale
	}
	if !nativeBoolEqual(basis.VATEnabled, movement.VATEnabled) || !nativeBoolEqual(basis.VATIncluded, movement.VATIncluded) {
		return ErrStale
	}
	return movement.validateReturnValues()
}

func (observation NativeDocumentObservation) Validate() error {
	if observation.SourceDeleted.Validate() != nil || observation.Scope.Validate() != nil || observation.Correlation.Validate() != nil || observation.Correlation.DocumentID == "" || !observation.Complete || !validID(observation.Revision) || observation.ObservedAt.IsZero() {
		return ErrInvalid
	}
	for _, id := range []string{observation.OrganizationID, observation.AgentID, observation.StoreID, observation.CurrencyID} {
		if !validUUID(id) {
			return ErrInvalid
		}
	}
	if !knownNativeBoolean(observation.Applicable) || observation.RateValue.Validate() != nil || observation.RateValue.IsKnown() && !positiveDecimal(*observation.RateValue.Value) || observation.Sum.Validate() != nil || !observation.Sum.IsKnown() || observation.Sum.Value.Validate() != nil {
		return ErrInvalid
	}
	if observation.CustomerOrderID.Validate() != nil || observation.DemandID.Validate() != nil || observation.Moment.Validate() != nil || !knownNativeBoolean(observation.VATEnabled) || !knownNativeBoolean(observation.VATIncluded) {
		return ErrInvalid
	}
	if len(observation.Positions) == 0 || len(observation.Positions) > 1000 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(observation.Positions))
	for _, position := range observation.Positions {
		if position.Validate() != nil || position.Price.Currency != observation.Sum.Value.Currency || seen[position.ID] {
			return ErrInvalid
		}
		seen[position.ID] = true
	}
	if observation.ReturnsComplete && (observation.ReturnsStartedAt.IsZero() || observation.ReturnsFinishedAt.Before(observation.ReturnsStartedAt)) {
		return ErrInvalid
	}
	return observation.validateReturns()
}

func (observation NativeDocumentObservation) validateReturns() error {
	if len(observation.Returns) > 25 || len(observation.Returns) > 0 && !observation.ReturnsComplete {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(observation.Returns))
	for _, entry := range observation.Returns {
		if entry.Validate(observation.Correlation.DocumentID, observation.Sum.Value.Currency) != nil || seen[entry.ID] {
			return ErrInvalid
		}
		seen[entry.ID] = true
	}
	return nil
}

func (position NativeDocumentPosition) Validate() error {
	if !validUUID(position.ID) || !validUUID(position.ItemID) || position.ItemKind != "product" && position.ItemKind != "variant" || !positiveDecimal(position.Quantity) || position.Price.Validate() != nil || !nonnegativeDecimal(position.Price.Amount) {
		return ErrInvalid
	}
	if position.Discount.Validate() != nil || !position.Discount.IsKnown() || position.Discount.Value.Validate() != nil || position.VAT.Validate() != nil || !position.VAT.IsKnown() || !nonnegativeDecimal(*position.VAT.Value) || !knownNativeBoolean(position.VATEnabled) {
		return ErrInvalid
	}
	return nil
}

func (movement NativeMovement) AllowedBy(policy *NativeDocumentPolicy, action Action, now time.Time) bool {
	if policy == nil || !movement.Applicable.IsKnown() || !policy.Allows(action, *movement.Applicable.Value, now) || movement.Event.OccurredAt.After(now.Add(time.Minute)) {
		return false
	}
	if movement.ParentEffect == NativeParentUnpostedDocument && !policy.AllowUnpostedParent {
		return false
	}
	if *movement.Applicable.Value && now.Sub(movement.Event.OccurredAt) > time.Duration(policy.EventMaxAgeSeconds)*time.Second {
		return false
	}
	for _, line := range movement.Lines {
		if !nonnegativeNotGreater(line.Quantity, policy.MaximumQuantity) {
			return false
		}
	}
	return true
}

func (evidence NativeReturnEvidence) Validate(demandID, currency string) error {
	if evidence.SourceDeleted.IsKnown() && evidence.SourceDeleted.Value != nil && *evidence.SourceDeleted.Value != "" {
		return ErrNativeDocumentDeleted
	}
	if !nativeDocumentPresent(evidence.SourceDeleted) || !validUUID(evidence.ID) || evidence.DemandID != demandID || !knownNativeBoolean(evidence.Applicable) || !validID(evidence.Revision) || evidence.ObservedAt.IsZero() || len(evidence.Positions) == 0 || len(evidence.Positions) > 1000 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(evidence.Positions))
	for _, position := range evidence.Positions {
		if position.Validate() != nil || position.Price.Currency != currency || seen[position.ID] {
			return ErrInvalid
		}
		seen[position.ID] = true
	}
	return nil
}

func (movement NativeMovement) validateOwnedLines(command Command) error {
	if !command.TargetPositionsComplete || len(command.TargetPositions) == 0 {
		return ErrBlocked
	}
	for _, line := range movement.Lines {
		bindingFound := false
		for _, binding := range movement.SourceBaseline.Bindings {
			if binding.Native.ID == line.OrderPositionID && binding.Source.Identity.Key() == line.Identity.Key() && positionMappingsEqual(binding.Source.Mapping, line.Mapping) {
				bindingFound = true
			}
		}
		if !bindingFound {
			return ErrStale
		}
		currentFound := false
		for _, current := range command.TargetPositions {
			if current.ID != line.OrderPositionID {
				continue
			}
			if currentFound || current.MoyskladKind != line.Mapping.MoyskladKind || current.MoyskladID != line.Mapping.MoyskladID || !nonnegativeNotGreater(line.Quantity, current.Quantity) {
				return ErrStale
			}
			currentFound = true
			if command.Action == ActionDemandCreate && (!current.Price.IsKnown() || !sameObservedPrice(Present(line.Price), current.Price) || !SameObservedDecimal(line.Discount, current.Discount) || !SameObservedDecimal(line.VAT, current.VAT) || !nativeBoolEqual(line.VATEnabled, current.VATEnabled)) {
				return ErrStale
			}
		}
		if !currentFound {
			return ErrStale
		}
	}
	return nil
}
