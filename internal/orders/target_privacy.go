package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// MarshalJSON keeps unowned native free text out of durable previews and audit
// payloads. The in-process observation still participates in conflict checks;
// its revision includes the complete provider document. This is a privacy mask,
// not evidence that the provider returned a missing or cleared field.
func (target TargetObservation) MarshalJSON() ([]byte, error) {
	type plain TargetObservation
	masked := plain(target)
	header, err := nativeHeaderRevision(target)
	if err != nil {
		return nil, err
	}
	masked.HeaderRevision = header
	masked.RedactedFields = append([]string{}, target.RedactedFields...)
	if target.Fields.Name.IsSpecified() {
		masked.RedactedFields = append(masked.RedactedFields, "fields.name")
	}
	if target.Fields.Description.IsSpecified() {
		masked.RedactedFields = append(masked.RedactedFields, "fields.description")
	}
	if len(target.Fields.Attributes) > 0 {
		masked.RedactedFields = append(masked.RedactedFields, "fields.attributes")
	}
	if target.Fields.ShipmentAddress.IsSpecified() {
		masked.RedactedFields = append(masked.RedactedFields, "fields.shipment_address")
	}
	if target.Fields.ShipmentAddressFull.IsSpecified() {
		masked.RedactedFields = append(masked.RedactedFields, "fields.shipment_address_full")
	}
	masked.Fields.ShipmentAddressFull = Field[ShipmentAddress]{Presence: PresenceMissing}
	masked.Fields.Name = Field[string]{Presence: PresenceMissing}
	masked.Fields.Description = Field[string]{Presence: PresenceMissing}
	masked.Fields.Attributes = nil
	masked.Fields.ShipmentAddress = Field[string]{Presence: PresenceMissing}
	return json.Marshal(masked)
}

func nativeHeaderRevision(target TargetObservation) (string, error) {
	if target.HeaderRevision != "" {
		// A sealed observation can contain a projected/redacted Fields view.
		return target.HeaderRevision, nil
	}
	if len(target.RedactedFields) > 0 {
		// Legacy redacted observations cannot acquire invented header evidence.
		return target.HeaderRevision, nil
	}
	fields := target.Fields
	fields.Positions = nil
	raw, err := json.Marshal(struct {
		Scope               Scope
		Correlation         Correlation
		Fields              CustomerOrderWrite
		ShipmentAddress     Field[string]
		ShipmentAddressFull Field[ShipmentAddress]
		TransientAttributes []AttributeWrite
	}{target.Scope, target.Correlation, fields, fields.ShipmentAddress, fields.ShipmentAddressFull, fields.TransientAttributes})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// WithHeaderRevision seals the observed header before a durable projection may
// replace its Fields. Provider decoders construct fresh observations without an
// incoming commitment; this is not an authorization supplied by a provider.
func (target TargetObservation) WithHeaderRevision() (TargetObservation, error) {
	header, err := nativeHeaderRevision(target)
	if err != nil {
		return target, err
	}
	target.HeaderRevision = header
	return target, nil
}

func nativeHeadersMatch(before, after TargetObservation) bool {
	left, err := nativeHeaderRevision(before)
	if err != nil || len(left) != sha256.Size*2 {
		return false
	}
	right, err := nativeHeaderRevision(after)
	return err == nil && left == right
}
