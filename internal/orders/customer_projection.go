package orders

import "strings"

// PIIAttributeMapping is the durable, value-free plan for projecting one
// protected customer field into a reviewed MoySklad string attribute. The
// address source targets the native shipmentAddress field instead of an
// attribute. Customer values are resolved only at plan/dispatch/readback time
// and never persist in a preview, command, receipt or audit payload.
type PIIAttributeMapping struct {
	Source      string `json:"source"`
	AttributeID string `json:"attribute_id,omitempty"`
}

func (mapping PIIAttributeMapping) Validate() error {
	if ShipmentAddressTarget(mapping.Source) != "" {
		if mapping.AttributeID != "" {
			return ErrInvalid
		}
		return nil
	}
	switch mapping.Source {
	case "customer.address":
		if mapping.AttributeID != "" {
			return ErrInvalid
		}
		return nil
	case "customer.name", "customer.phone", "customer.email":
		if !validUUID(mapping.AttributeID) {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
}

func (write CustomerOrderWrite) HasPIIMappings() bool {
	return len(write.PIIMappings) > 0
}

func (write CustomerOrderWrite) HasShipmentAddressMapping() bool {
	for _, mapping := range write.PIIMappings {
		if mapping.Source == "customer.address" {
			return true
		}
	}
	return false
}

func (write CustomerOrderWrite) customerAttributeIDs() map[string]bool {
	result := make(map[string]bool, len(write.PIIMappings))
	for _, mapping := range write.PIIMappings {
		if mapping.Source != "customer.address" && ShipmentAddressTarget(mapping.Source) == "" {
			result[mapping.AttributeID] = true
		}
	}
	return result
}

// CombinedAttributes returns persistent and transient values for comparison
// only. Transient values must never be serialized or logged.
func (write CustomerOrderWrite) CombinedAttributes() []AttributeWrite {
	result := make([]AttributeWrite, 0, len(write.Attributes)+len(write.TransientAttributes))
	result = append(result, write.Attributes...)
	return append(result, write.TransientAttributes...)
}

// ProjectCustomer resolves only the explicitly reviewed mappings. Missing
// provider values are omitted rather than fabricated; duplicate destination
// attributes are rejected. The returned values are transient and must never be
// logged or persisted outside the protected dispatch phase.
func ProjectCustomer(customer Customer, mappings []PIIAttributeMapping) ([]AttributeWrite, Field[string], error) {
	attributes := make([]AttributeWrite, 0, len(mappings))
	address := Field[string]{Presence: PresenceMissing}
	seen := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		if err := mapping.Validate(); err != nil || seen[mapping.Source] {
			return nil, Field[string]{}, ErrInvalid
		}
		seen[mapping.Source] = true
		if ShipmentAddressTarget(mapping.Source) != "" {
			continue
		}
		if mapping.Source == "customer.address" {
			address = customerAddressText(customer.Address)
			continue
		}
		if seen[mapping.AttributeID] {
			return nil, Field[string]{}, ErrInvalid
		}
		seen[mapping.AttributeID] = true
		value, supported := customerAttribute(customer, mapping.Source)
		if !supported {
			return nil, Field[string]{}, ErrInvalid
		}
		if value.IsKnown() {
			attributes = append(attributes, AttributeWrite{AttributeID: mapping.AttributeID, Value: value})
		}
	}
	return attributes, address, nil
}

func customerAttribute(customer Customer, source string) (Field[string], bool) {
	switch source {
	case "customer.name":
		return customer.Name, true
	case "customer.phone":
		return customer.Phone, true
	case "customer.email":
		return customer.Email, true
	default:
		return Field[string]{}, false
	}
}

func customerAddressText(address Field[Address]) Field[string] {
	if !address.IsKnown() {
		return Field[string]{Presence: address.Presence}
	}
	parts := make([]string, 0, 7)
	for _, field := range []Field[string]{
		address.Value.Country,
		address.Value.Region,
		address.Value.City,
		address.Value.Street,
		address.Value.House,
		address.Value.Apartment,
		address.Value.PostalCode,
	} {
		if field.IsKnown() && strings.TrimSpace(*field.Value) != "" {
			parts = append(parts, strings.TrimSpace(*field.Value))
		}
	}
	if len(parts) == 0 {
		return Field[string]{Presence: PresenceEmpty}
	}
	return Present(strings.Join(parts, ", "))
}
