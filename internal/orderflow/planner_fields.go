package orderflow

import (
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func mirrorWrite(posting orders.Posting, review orders.ReviewContext, positions []orders.PositionWrite) (orders.CustomerOrderWrite, []orders.Validation) {
	config := review.Config
	write := orders.CustomerOrderWrite{CurrencyID: config.CurrencyID, OrganizationID: config.OrganizationID, AgentID: config.AgentID,
		StoreID: config.StoreID, StateID: postingState(posting, config), SalesChannelID: config.SalesChannelID, Positions: positions}
	validations := make([]orders.Validation, 0)
	for _, mapping := range config.FieldMappings {
		if validation := applyMirrorMapping(&write, posting, review.Metadata, mapping); validation.Code != "" {
			validations = append(validations, validation)
		}
	}
	if !mappedTarget(config, "positions.vat") {
		for index := range write.Positions {
			write.Positions[index].VAT = orders.Field[orders.Decimal]{}
		}
	}
	if config.CurrencyID == "" {
		validations = append(validations, orders.Validation{Field: "currency_id", Code: "native_currency_required"})
	}
	if posting.Status.IsKnown() && *posting.Status.Value == "cancelled" {
		validations = append(validations, historicalCancellationValidation(write)...)
	}
	return write, validations
}

func applyMirrorMapping(write *orders.CustomerOrderWrite, posting orders.Posting, metadata orders.MetadataRegistry, mapping orders.FieldMapping) orders.Validation {
	if strings.HasPrefix(mapping.Source, "customer.") {
		return orders.Validation{Field: mapping.Source, Code: "protected_customer_projection_unavailable"}
	}
	switch mapping.Target {
	case "name", "description", "attribute":
		return applyTextMapping(write, posting, metadata, mapping)
	case "moment", "delivery_at":
		return applyTimeMapping(write, posting, mapping)
	case "applicable", "vat_enabled", "vat_included":
		return applyBooleanMapping(write, posting, mapping)
	case "positions.quantity", "positions.price", "positions.vat", "positions.reserve":
		return applyPositionMapping(write, mapping)
	case "state":
		if mapping.Source == "status" || mapping.Source == "posting.status" {
			return orders.Validation{}
		}
		return unsupportedMapping(mapping)
	default:
		return unsupportedMapping(mapping)
	}
}

func applyTextMapping(write *orders.CustomerOrderWrite, posting orders.Posting, metadata orders.MetadataRegistry, mapping orders.FieldMapping) orders.Validation {
	value, supported := postingText(posting, mapping.Source)
	if !supported {
		return unsupportedMapping(mapping)
	}
	if !value.IsKnown() {
		return orders.Validation{}
	}
	switch mapping.Target {
	case "name":
		write.Name = value
	case "description":
		write.Description = value
	case "attribute":
		if !metadataAttributePresent(metadata, mapping.AttributeID, posting.Scope) {
			return orders.Validation{Field: mapping.Source, Code: "native_attribute_unverified"}
		}
		write.Attributes = append(write.Attributes, orders.AttributeWrite{AttributeID: mapping.AttributeID, Value: value})
	}
	return orders.Validation{}
}

func postingText(posting orders.Posting, source string) (orders.Field[string], bool) {
	switch strings.TrimPrefix(source, "posting.") {
	case "posting_number", "number":
		return orders.Present(posting.PostingNumber), true
	case "commercial_order_id", "order_id":
		return orders.Present(posting.CommercialOrderID), true
	case "order_number":
		return posting.OrderNumber, true
	case "status":
		return posting.Status, true
	case "substatus":
		return posting.Substatus, true
	case "scheme":
		return posting.Scheme, true
	case "warehouse_id":
		return posting.WarehouseID, true
	case "delivery_method_id":
		return posting.DeliveryMethodID, true
	default:
		return orders.Field[string]{}, false
	}
}

func applyTimeMapping(write *orders.CustomerOrderWrite, posting orders.Posting, mapping orders.FieldMapping) orders.Validation {
	value, supported := postingTime(posting, mapping.Source)
	if !supported {
		return unsupportedMapping(mapping)
	}
	if !value.IsKnown() {
		return orders.Validation{}
	}
	if mapping.Target == "moment" {
		write.Moment = value
		return orders.Validation{}
	}
	write.DeliveryAt = value
	return orders.Validation{}
}

func postingTime(posting orders.Posting, source string) (orders.Field[time.Time], bool) {
	source = strings.TrimPrefix(strings.TrimPrefix(source, "posting."), "times.")
	switch source {
	case "created_at":
		return posting.Times.CreatedAt, true
	case "in_process_at":
		return posting.Times.InProcessAt, true
	case "shipment_at":
		return posting.Times.ShipmentAt, true
	case "delivering_at":
		return posting.Times.DeliveringAt, true
	case "delivered_at":
		return posting.Times.DeliveredAt, true
	case "shipment_without_delay_at":
		return posting.Times.ShipmentWithoutDelayAt, true
	default:
		return orders.Field[time.Time]{}, false
	}
}

func applyBooleanMapping(write *orders.CustomerOrderWrite, posting orders.Posting, mapping orders.FieldMapping) orders.Validation {
	var value orders.Field[bool]
	switch mapping.Source {
	case "constant.false":
		value = orders.Present(false)
	case "constant.true":
		value = orders.Present(true)
	case "posting.cancelled", "cancelled":
		if mapping.Target != "applicable" || !posting.Status.IsKnown() {
			return unsupportedMapping(mapping)
		}
		value = orders.Present(*posting.Status.Value != "cancelled")
	default:
		return unsupportedMapping(mapping)
	}
	switch mapping.Target {
	case "applicable":
		write.Applicable = value
	case "vat_enabled":
		write.VATEnabled = value
	case "vat_included":
		write.VATIncluded = value
	}
	return orders.Validation{}
}

func applyPositionMapping(write *orders.CustomerOrderWrite, mapping orders.FieldMapping) orders.Validation {
	if mapping.Target != "positions.reserve" {
		field := strings.TrimPrefix(mapping.Target, "positions.")
		source := strings.TrimPrefix(mapping.Source, "posting.")
		if source == "lines."+field || source == "products."+field {
			return orders.Validation{}
		}
		return unsupportedMapping(mapping)
	}
	if mapping.Source != "constant.zero" {
		return orders.Validation{Field: mapping.Source, Code: "reservation_policy_not_authorized"}
	}
	for index := range write.Positions {
		write.Positions[index].Reserve = orders.Present(orders.Decimal("0"))
	}
	return orders.Validation{}
}

func historicalCancellationValidation(write orders.CustomerOrderWrite) []orders.Validation {
	result := make([]orders.Validation, 0)
	if !write.Applicable.IsKnown() || *write.Applicable.Value {
		result = append(result, orders.Validation{Field: "applicable", Code: "cancelled_mirror_requires_explicit_false"})
	}
	for _, position := range write.Positions {
		if !position.Reserve.IsKnown() || !decimalEqual(*position.Reserve.Value, "0") {
			return append(result, orders.Validation{Field: "positions.reserve", Code: "cancelled_mirror_requires_explicit_zero_reserve"})
		}
	}
	return result
}

func postingState(posting orders.Posting, config orders.Config) string {
	if !posting.Status.IsKnown() {
		return config.StateID
	}
	for _, mapping := range config.StatusMappings {
		if mapping.Status == *posting.Status.Value && sameField(mapping.Substatus, posting.Substatus) {
			return mapping.StateID
		}
	}
	for _, mapping := range config.StatusMappings {
		if mapping.Status == *posting.Status.Value && mapping.Substatus.IsMissing() {
			return mapping.StateID
		}
	}
	return config.StateID
}

func mappedTarget(config orders.Config, target string) bool {
	return slices.ContainsFunc(config.FieldMappings, func(mapping orders.FieldMapping) bool { return mapping.Target == target })
}

func metadataAttributePresent(metadata orders.MetadataRegistry, id string, scope orders.Scope) bool {
	return metadata.Scope == scope && metadata.Validate() == nil && slices.ContainsFunc(metadata.Attributes, func(attribute orders.AttributeDefinition) bool {
		return attribute.AttributeID == id
	})
}

func unsupportedMapping(mapping orders.FieldMapping) orders.Validation {
	return orders.Validation{Field: mapping.Source, Code: "field_mapping_source_unsupported"}
}
