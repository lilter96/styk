package orders

import "encoding/json"

// MarshalJSON keeps unowned native free text out of durable previews and audit
// payloads. The in-process observation still participates in conflict checks;
// its revision includes the complete provider document. This is a privacy mask,
// not evidence that the provider returned a missing or cleared field.
func (target TargetObservation) MarshalJSON() ([]byte, error) {
	type plain TargetObservation
	masked := plain(target)
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
