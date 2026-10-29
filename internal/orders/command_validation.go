package orders

import "fmt"

func (action Action) Valid() bool {
	switch action {
	case ActionMirrorCreate, ActionMirrorUpdate, ActionPostingPack, ActionPostingShip, ActionPostingCancel, ActionPostingDeliver:
		return true
	default:
		return false
	}
}

func (action Action) IsMirror() bool {
	return action == ActionMirrorCreate || action == ActionMirrorUpdate
}

func (command Command) Validate() error {
	if command.Scope.Validate() != nil || !validID(command.ID) || !command.Action.Valid() || !validID(command.CommercialOrderID) || !validID(command.PostingNumber) {
		return ErrInvalid
	}
	if command.ConfigRevision < 1 || command.Credentials.Ozon < 1 || command.Credentials.Moysklad < 1 || command.CreatedAt.IsZero() {
		return ErrInvalid
	}
	if !validID(command.IdempotencyKey) || !validID(command.ExpectedSourceRevision) || !command.SourceOrigin.Valid() {
		return ErrInvalid
	}
	if command.Correlation.Scope != command.Scope || command.Correlation.PostingNumber != command.PostingNumber {
		return ErrStale
	}
	if err := command.Correlation.Validate(); err != nil {
		return fmt.Errorf("validate order correlation: %w", err)
	}
	if command.Warehouse.Scope != command.Scope || command.Warehouse.Validate() != nil {
		return ErrStale
	}
	if command.Action.IsMirror() {
		return command.validateMirror()
	}
	return command.validatePostingAction()
}

func (command Command) validateMirror() error {
	if command.CustomerOrder == nil || command.PostingAction != nil || command.SourceOrigin != OriginOzon {
		return ErrInvalid
	}
	if command.Action == ActionMirrorCreate && command.Correlation.CustomerOrderID != "" {
		return ErrConflict
	}
	if command.Action == ActionMirrorUpdate && (!validID(command.Correlation.CustomerOrderID) || !validID(command.ExpectedTargetRevision)) {
		return ErrInvalid
	}
	if command.CustomerOrder.StoreID != command.Warehouse.MoyskladStoreID {
		return ErrStale
	}
	return command.CustomerOrder.Validate(command.Scope)
}

func (command Command) validatePostingAction() error {
	if command.CustomerOrder != nil || command.PostingAction == nil || command.SourceOrigin == OriginSTYK {
		return ErrInvalid
	}
	action := command.PostingAction
	if action.Capability.Validate() != nil || action.Capability.WarehouseID != command.Warehouse.OzonWarehouseID {
		return ErrStale
	}
	if action.Capability.ID != command.Warehouse.Capability.ID || action.Capability.Revision != command.Warehouse.Capability.Revision {
		return ErrStale
	}
	if err := ValidateLineIdentities(action.Lines); err != nil {
		return err
	}
	return optionalField(action.ReasonID, validText)
}

func (write CustomerOrderWrite) Validate(scope Scope) error {
	for _, id := range []string{write.OrganizationID, write.AgentID, write.StoreID, write.StateID, write.SalesChannelID} {
		if !validID(id) {
			return ErrInvalid
		}
	}
	if err := write.validateFields(); err != nil {
		return err
	}
	if len(write.Positions) == 0 {
		return ErrBlocked
	}
	identities := make([]LineIdentity, 0, len(write.Positions))
	for _, position := range write.Positions {
		if err := position.Validate(scope); err != nil {
			return err
		}
		identities = append(identities, position.Identity)
	}
	return ValidateLineIdentities(identities)
}

func (write CustomerOrderWrite) validateFields() error {
	if write.Name.Validate() != nil || write.Description.Validate() != nil || write.Applicable.Validate() != nil || write.VATEnabled.Validate() != nil || write.VATIncluded.Validate() != nil {
		return ErrInvalid
	}
	if optionalField(write.Moment, validTime) != nil || optionalField(write.DeliveryAt, validTime) != nil {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(write.Attributes))
	for _, attribute := range write.Attributes {
		if !validUUID(attribute.AttributeID) || seen[attribute.AttributeID] || attribute.Value.Validate() != nil {
			return ErrInvalid
		}
		seen[attribute.AttributeID] = true
	}
	return nil
}

func (position PositionWrite) Validate(scope Scope) error {
	if position.Identity.Validate() != nil || !positiveDecimal(position.Quantity) || position.Mapping.Scope != scope {
		return ErrInvalid
	}
	if err := position.Mapping.Validate(); err != nil {
		return err
	}
	if !position.Mapping.Matches(position.Identity) {
		return ErrStale
	}
	if err := optionalField(position.Reserve, validNonnegativeDecimal); err != nil {
		return err
	}
	if err := optionalField(position.Price, func(value Money) error { return value.Validate() }); err != nil {
		return err
	}
	return optionalField(position.VAT, validNonnegativeDecimal)
}

func (mapping ProductMappingRef) Validate() error {
	if mapping.Scope.Validate() != nil || !validID(mapping.ID) || mapping.Version < 1 || !mapping.Confirmed || mapping.ConfirmedAt.IsZero() {
		return ErrInvalid
	}
	if !validID(mapping.OzonProductID) || !validID(mapping.MoyskladID) || (mapping.SKU == "" && mapping.OfferID == "") {
		return ErrInvalid
	}
	if mapping.MoyskladKind != "product" && mapping.MoyskladKind != "variant" {
		return ErrBlocked
	}
	return nil
}

func (mapping ProductMappingRef) Matches(identity LineIdentity) bool {
	if mapping.Validate() != nil || identity.Validate() != nil {
		return false
	}
	if identity.SKU != "" && mapping.SKU != "" && identity.SKU != mapping.SKU {
		return false
	}
	if identity.OfferID != "" {
		return mapping.OfferID != "" && identity.OfferID == mapping.OfferID
	}
	return identity.SKU != "" && mapping.SKU == identity.SKU
}

func (mapping WarehouseMappingRef) Validate() error {
	if mapping.Scope.Validate() != nil || !validID(mapping.ID) || mapping.Version < 1 || !mapping.Confirmed || mapping.ConfirmedAt.IsZero() {
		return ErrInvalid
	}
	if !validID(mapping.OzonWarehouseID) || !validID(mapping.MoyskladStoreID) {
		return ErrInvalid
	}
	if mapping.Capability.ID != "" && (mapping.Capability.Validate() != nil || mapping.Capability.WarehouseID != mapping.OzonWarehouseID) {
		return ErrStale
	}
	return nil
}
