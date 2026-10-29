package orders

import (
	"fmt"
	"strings"
	"time"
)

func (posting Posting) Validate() error {
	if posting.Scheme.Validate() != nil {
		return ErrInvalid
	}
	if posting.Scope.Validate() != nil || !validID(posting.CommercialOrderID) || !validID(posting.PostingNumber) {
		return ErrInvalid
	}
	if err := posting.Evidence.Validate(); err != nil {
		return fmt.Errorf("validate posting evidence: %w", err)
	}
	if err := posting.validateFacts(); err != nil {
		return err
	}
	if err := posting.validateRelations(); err != nil {
		return err
	}
	if err := posting.validateLines(); err != nil {
		return err
	}
	for _, drift := range posting.Drift {
		if !safePath(drift.Path) || !validID(drift.Code) {
			return ErrInvalid
		}
	}
	return nil
}

func (order Order) Validate() error {
	if order.Scope.Validate() != nil || !validID(order.CommercialOrderID) {
		return ErrInvalid
	}
	if err := optionalField(order.OrderNumber, validText); err != nil {
		return err
	}
	seen := make(map[string]bool, len(order.PostingNumbers))
	for _, number := range order.PostingNumbers {
		if !validID(number) || seen[number] {
			return ErrConflict
		}
		seen[number] = true
	}
	return nil
}

func (posting Posting) validateRelations() error {
	if posting.ParentPostingNumber.IsKnown() && *posting.ParentPostingNumber.Value == posting.PostingNumber {
		return ErrConflict
	}
	seen := make(map[string]bool, len(posting.ChildPostingNumbers))
	for _, number := range posting.ChildPostingNumbers {
		if !validID(number) || number == posting.PostingNumber || seen[number] {
			return ErrConflict
		}
		seen[number] = true
	}
	return nil
}

func (posting Posting) validateFacts() error {
	fields := []Field[string]{posting.ParentPostingNumber, posting.OrderNumber, posting.Status, posting.Substatus, posting.WarehouseID, posting.DeliveryMethodID}
	for _, field := range fields {
		if err := optionalField(field, validText); err != nil {
			return err
		}
	}
	if err := posting.Times.Validate(); err != nil {
		return err
	}
	if err := optionalField(posting.Cancellation, func(value Cancellation) error { return value.Validate() }); err != nil {
		return err
	}
	if posting.DeliveryProfile != nil {
		return posting.DeliveryProfile.Validate()
	}
	return nil
}

func (posting Posting) validateLines() error {
	identities := make([]LineIdentity, 0, len(posting.Lines))
	for _, line := range posting.Lines {
		if err := line.Validate(); err != nil {
			return fmt.Errorf("validate posting line: %w", err)
		}
		identities = append(identities, line.Identity)
	}
	return ValidateLineIdentities(identities)
}

func (line PostingLine) Validate() error {
	if line.Identity.Validate() != nil || !nonnegativeDecimal(line.Quantity) {
		return ErrInvalid
	}
	if err := optionalField(line.ProductID, validText); err != nil {
		return err
	}
	if err := optionalField(line.Name, validText); err != nil {
		return err
	}
	if err := optionalField(line.CancelledQuantity, validNonnegativeDecimal); err != nil {
		return err
	}
	if err := optionalField(line.Price, func(value Money) error { return value.Validate() }); err != nil {
		return err
	}
	return optionalField(line.VAT, validNonnegativeDecimal)
}

func (times PostingTimes) Validate() error {
	fields := []Field[time.Time]{times.CreatedAt, times.InProcessAt, times.ShipmentAt, times.DeliveringAt, times.DeliveredAt, times.ShipmentWithoutDelayAt}
	for _, field := range fields {
		if err := optionalField(field, validTime); err != nil {
			return err
		}
	}
	return nil
}

func (evidence EvidenceRevision) Validate() error {
	if !validID(evidence.Revision) || evidence.ObservedAt.IsZero() || !evidence.Origin.Valid() {
		return ErrInvalid
	}
	if evidence.Origin == OriginSTYK && !validID(evidence.SourceCommandID) {
		return ErrInvalid
	}
	if evidence.SourceCommandID != "" && !validID(evidence.SourceCommandID) {
		return ErrInvalid
	}
	if evidence.LocalSequence < 0 || (evidence.CollectionKind == CollectionSerialDetail && (!validID(evidence.CollectionID) || evidence.LocalSequence < 1)) {
		return ErrInvalid
	}
	if evidence.CollectionKind != "" && evidence.CollectionKind != CollectionSnapshot && evidence.CollectionKind != CollectionSerialDetail {
		return ErrInvalid
	}
	if err := optionalField(evidence.ProviderVersion, validText); err != nil {
		return err
	}
	return optionalField(evidence.ProviderUpdatedAt, validTime)
}

func (origin Origin) Valid() bool {
	switch origin {
	case OriginOzon, OriginMoysklad, OriginUser, OriginSTYK:
		return true
	default:
		return false
	}
}

// Active is only a refresh scheduling hint. Unrecognized or absent statuses
// remain active; this method neither authorizes commands nor releases reserve.
func (posting Posting) Active() bool {
	if !posting.Status.IsKnown() || posting.Status.Value == nil {
		return true
	}
	return *posting.Status.Value != "delivered" && *posting.Status.Value != "cancelled"
}

func (cancellation Cancellation) Validate() error {
	fields := []Field[string]{cancellation.ReasonID, cancellation.Type, cancellation.Initiator}
	for _, field := range fields {
		if err := optionalField(field, validText); err != nil {
			return err
		}
	}
	return cancellation.AfterShipment.Validate()
}

func (capability CapabilityRef) Validate() error {
	if !validID(capability.ID) || capability.Revision < 1 || !validID(capability.WarehouseID) || !validID(capability.DeliveryMethodID) {
		return ErrInvalid
	}
	if capability.Scheme != "FBS" && capability.Scheme != "rFBS" {
		return ErrBlocked
	}
	if capability.ObservedAt.IsZero() || !capability.ValidUntil.After(capability.ObservedAt) {
		return ErrInvalid
	}
	if err := optionalField(capability.Carrier, validClassifier); err != nil {
		return err
	}
	if err := optionalField(capability.Destination, validClassifier); err != nil {
		return err
	}
	seen := make(map[string]bool, len(capability.VerifiedDimensions))
	for _, dimension := range capability.VerifiedDimensions {
		if !validDimension(dimension) || seen[dimension] {
			return ErrInvalid
		}
		seen[dimension] = true
	}
	return capability.Express.Validate()
}

func validDimension(dimension string) bool {
	switch dimension {
	case "scheme", "warehouse", "carrier", "express", "destination":
		return true
	default:
		return false
	}
}

func validClassifier(value string) error {
	if len(value) == 0 || len(value) > 64 {
		return ErrInvalid
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || strings.ContainsRune("_-", char) {
			continue
		}
		return ErrInvalid
	}
	return nil
}

func optionalField[T any](field Field[T], validate func(T) error) error {
	if err := field.Validate(); err != nil {
		return err
	}
	if !field.IsKnown() {
		return nil
	}
	return validate(*field.Value)
}

func validText(value string) error {
	if !validID(value) {
		return ErrInvalid
	}
	return nil
}

func validTime(value time.Time) error {
	if value.IsZero() {
		return ErrInvalid
	}
	return nil
}

func validNonnegativeDecimal(value Decimal) error {
	if !nonnegativeDecimal(value) {
		return ErrInvalid
	}
	return nil
}
