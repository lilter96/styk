package orders

import "encoding/hex"

func (movement NativeMovement) validateParentEffect(command Command) error {
	if movement.ParentApplicable.Validate() != nil {
		return ErrInvalid
	}
	if movement.Consumption != nil && movement.Consumption.Validate() != nil {
		return ErrInvalid
	}
	switch movement.ParentEffect {
	case NativeParentShippedCounter:
		return nil
	case NativeParentUnpostedDocument:
		if movement.Consumption == nil || !nativeUnpostedSelected(command) {
			return ErrBlocked
		}
		return nil
	default:
		return ErrBlocked
	}
}

// Validate checks only the bounded seal shape. It never proves consumption or
// authorizes dispatch; the store must recompute it from complete scoped facts.
func (seal NativeConsumptionSeal) Validate() error {
	if !nativeConsumptionHash(seal.Digest) || seal.Positions == nil || len(seal.Positions) > 1000 || seal.Events == nil || len(seal.Events) > MaxNativeConsumptionEvents {
		return ErrInvalid
	}
	positions := make(map[string]bool, len(seal.Positions))
	for _, position := range seal.Positions {
		if !validUUID(position.OrderPositionID) || positions[position.OrderPositionID] || !nonnegativeDecimal(position.GrossDemand) {
			return ErrInvalid
		}
		positions[position.OrderPositionID] = true
	}
	works, documents := make(map[string]bool, len(seal.Events)), make(map[string]bool, len(seal.Events))
	for _, event := range seal.Events {
		key := event.Kind + "/" + event.DocumentID
		if works[event.WorkID] || documents[key] || !nativeConsumedEventValid(event) {
			return ErrInvalid
		}
		works[event.WorkID], documents[key] = true, true
	}
	return nil
}

func nativeConsumedEventValid(event NativeConsumedEvent) bool {
	if !validID(event.WorkID) || !validUUID(event.DocumentID) || !nativeConsumptionHash(event.CommandFingerprint) || len(event.Lines) == 0 || len(event.Lines) > 100 {
		return false
	}
	if event.Kind != "demand" && event.Kind != "salesreturn" || event.Kind == "salesreturn" && !validID(event.DemandWorkID) || event.Kind == "demand" && event.DemandWorkID != "" {
		return false
	}
	seen := make(map[string]bool, len(event.Lines))
	for _, line := range event.Lines {
		if !validUUID(line.OrderPositionID) || seen[line.OrderPositionID] || !positiveDecimal(line.Quantity) || event.Kind == "salesreturn" && !validUUID(line.BasisPositionID) || event.Kind == "demand" && line.BasisPositionID != "" {
			return false
		}
		seen[line.OrderPositionID] = true
	}
	return true
}

func nativeConsumptionHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}
