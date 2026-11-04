package orders

import (
	"strings"
	"unicode/utf8"
)

// ShipmentAddress is native delivery data. Country and region are destination
// account references, never inferred from recipient free text.
// CustomerOrderWrite deliberately excludes this transient value from JSON.
type ShipmentAddressValueError struct{ Field, Code string }

func (e *ShipmentAddressValueError) Error() string {
	return "shipment address component invalid: " + e.Field + ":" + e.Code
}

type ShipmentAddress struct {
	City       Field[string] `json:"city"`
	Street     Field[string] `json:"street"`
	House      Field[string] `json:"house"`
	Apartment  Field[string] `json:"apartment"`
	PostalCode Field[string] `json:"postal_code"`
	AddInfo    Field[string] `json:"add_info"`
	Comment    Field[string] `json:"comment"`
	CountryID  Field[string] `json:"country_id"`
	RegionID   Field[string] `json:"region_id"`
}

func ShipmentAddressTarget(source string) string {
	switch source {
	case "customer.address.city", "customer.address.street", "customer.address.house", "customer.address.apartment", "customer.address.postal_code":
		return "shipment_address_full." + strings.TrimPrefix(source, "customer.address.")
	default:
		return ""
	}
}
func ShipmentAddressTargetValid(target string) bool {
	return ShipmentAddressTarget("customer.address."+strings.TrimPrefix(target, "shipment_address_full.")) == target && strings.HasPrefix(target, "shipment_address_full.")
}
func (a ShipmentAddress) Fields() map[string]Field[string] {
	return map[string]Field[string]{"city": a.City, "street": a.Street, "house": a.House, "apartment": a.Apartment, "postal_code": a.PostalCode, "add_info": a.AddInfo, "comment": a.Comment, "country_id": a.CountryID, "region_id": a.RegionID}
}
func (a *ShipmentAddress) Set(key string, value Field[string]) {
	switch key {
	case "city":
		a.City = value
	case "street":
		a.Street = value
	case "house":
		a.House = value
	case "apartment":
		a.Apartment = value
	case "postal_code":
		a.PostalCode = value
	case "add_info":
		a.AddInfo = value
	case "comment":
		a.Comment = value
	case "country_id":
		a.CountryID = value
	case "region_id":
		a.RegionID = value
	}
}
func (a ShipmentAddress) Validate() error {
	for key, value := range a.Fields() {
		if value.Validate() != nil {
			return &ShipmentAddressValueError{Field: key, Code: "shipment_address_component_invalid"}
		}
		if key == "country_id" || key == "region_id" {
			if value.IsKnown() && !validUUID(*value.Value) {
				return ErrInvalid
			}
			continue
		}
		maximum := 255
		if key == "house" || key == "apartment" {
			maximum = 30
		}
		if key == "postal_code" {
			maximum = 6
		}
		if value.IsKnown() && utf8.RuneCountInString(*value.Value) > maximum {
			return &ShipmentAddressValueError{Field: key, Code: "shipment_address_component_too_long"}
		}
		if value.IsKnown() && (!utf8.ValidString(*value.Value) || utf8.RuneCountInString(*value.Value) > maximum || strings.ContainsAny(*value.Value, "\x00\r\n")) {
			return ErrInvalid
		}
	}
	return nil
}
func (write CustomerOrderWrite) HasShipmentAddressFullMapping() bool {
	for _, m := range write.PIIMappings {
		if ShipmentAddressTarget(m.Source) != "" {
			return true
		}
	}
	return false
}
func ProjectShipmentAddressFull(customer Customer, mappings []PIIAttributeMapping) (Field[ShipmentAddress], error) {
	result := ShipmentAddress{}
	if !customer.Address.IsKnown() {
		return Field[ShipmentAddress]{Presence: customer.Address.Presence}, nil
	}
	source := customer.Address.Value
	values := map[string]Field[string]{"city": source.City, "street": source.Street, "house": source.House, "apartment": source.Apartment, "postal_code": source.PostalCode}
	found := false
	for _, mapping := range mappings {
		target := ShipmentAddressTarget(mapping.Source)
		if target == "" {
			continue
		}
		field := strings.TrimPrefix(target, "shipment_address_full.")
		value := values[field]
		if !value.IsKnown() && value.Presence != PresenceEmpty {
			continue
		}
		result.Set(field, value)
		found = true
	}
	if !found {
		return Field[ShipmentAddress]{Presence: PresenceMissing}, nil
	}
	return Present(result), result.Validate()
}
func (write *CustomerOrderWrite) ApplyCustomerProjection(customer Customer) error {
	attributes, address, err := ProjectCustomer(customer, write.PIIMappings)
	if err != nil {
		return err
	}
	full, err := ProjectShipmentAddressFull(customer, write.PIIMappings)
	if err != nil {
		return err
	}
	write.TransientAttributes, write.ShipmentAddress, write.ShipmentAddressFull = attributes, address, full
	return nil
}
func ShipmentAddressChangedFields(wanted, actual CustomerOrderWrite) []string {
	result := make([]string, 0)
	if !wanted.ShipmentAddressFull.IsKnown() {
		return result
	}
	values := map[string]Field[string]{}
	if actual.ShipmentAddressFull.IsKnown() {
		values = actual.ShipmentAddressFull.Value.Fields()
	}
	for _, m := range wanted.PIIMappings {
		target := ShipmentAddressTarget(m.Source)
		if target == "" {
			continue
		}
		key := strings.TrimPrefix(target, "shipment_address_full.")
		value := wanted.ShipmentAddressFull.Value.Fields()[key]
		current := values[key]
		if value.IsKnown() && (!current.IsKnown() || *current.Value != *value.Value) || value.Presence == PresenceEmpty && current.Presence != PresenceEmpty {
			result = append(result, target)
		}
	}
	return result
}
