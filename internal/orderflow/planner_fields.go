package orderflow

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func mirrorWrite(ctx context.Context, posting orders.Posting, review orders.ReviewContext, positions []orders.PositionWrite, customerReader orders.CustomerReader) (orders.CustomerOrderWrite, []orders.Validation) {
	config := review.Config
	write := orders.CustomerOrderWrite{CurrencyID: config.CurrencyID, OrganizationID: config.OrganizationID, AgentID: config.AgentID,
		StoreID: config.StoreID, StateID: postingState(posting, config), SalesChannelID: config.SalesChannelID, Positions: positions}
	validations := make([]orders.Validation, 0)
	for _, mapping := range config.FieldMappings {
		if validation := applyMirrorMapping(&write, posting, review, mapping, customerReader); validation.Code != "" {
			validations = append(validations, validation)
		}
	}
	if validation := applyConfiguredReserve(&write, posting, review); validation.Code != "" {
		validations = append(validations, validation)
	}
	if len(validations) == 0 {
		validations = append(validations, resolveCustomerProjection(ctx, &write, posting, customerReader)...)
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

func applyMirrorMapping(write *orders.CustomerOrderWrite, posting orders.Posting, review orders.ReviewContext, mapping orders.FieldMapping, customerReader orders.CustomerReader) orders.Validation {
	if strings.HasPrefix(mapping.Source, "customer.") {
		return applyCustomerMapping(write, posting, review, mapping)
	}
	switch mapping.Target {
	case "name", "description", "attribute":
		return applyTextMapping(write, posting, review.Metadata, mapping)
	case "moment", "delivery_at":
		return applyTimeMapping(write, posting, mapping)
	case "applicable", "vat_enabled", "vat_included":
		return applyBooleanMapping(write, posting, mapping)
	case "positions.quantity", "positions.price", "positions.vat", "positions.reserve":
		if mapping.Target == "positions.reserve" && mapping.Source == orders.ReserveOpenQuantitySource {
			return orders.Validation{}
		}
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

func applyTimeMapping(write *orders.CustomerOrderWrite, posting orders.Posting, mapping orders.FieldMapping) orders.Validation {
	value, supported := postingTime(posting, mapping.Source)
	if !supported {
		return unsupportedMapping(mapping)
	}
	if !value.IsKnown() {
		return orders.Validation{}
	}
	if mapping.Target == "moment" {
		write.Moment = orders.Present(orders.NativeAccountingMoment(*value.Value))
		return orders.Validation{}
	}
	write.DeliveryAt = orders.Present(orders.NativeAccountingMoment(*value.Value))
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
		if source == "lines."+field || source == "products."+field || source == "positions."+field {
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

func applyCustomerMapping(write *orders.CustomerOrderWrite, posting orders.Posting, review orders.ReviewContext, mapping orders.FieldMapping) orders.Validation {
	if !review.Config.PIIEnabled {
		return orders.Validation{Field: mapping.Source, Code: "customer_pii_disabled"}
	}
	if posting.Customer.Presence != orders.PresenceValue {
		// No protected reference exists, so the reviewed mapping has no value
		// to project. The mirror may still proceed without customer details.
		return orders.Validation{}
	}
	if mapping.Target == "attribute" && !metadataAttributePresent(review.Metadata, mapping.AttributeID, posting.Scope) {
		return orders.Validation{Field: mapping.Source, Code: "native_attribute_unverified"}
	}
	projection := orders.PIIAttributeMapping{Source: mapping.Source}
	if mapping.Target == "attribute" {
		projection.AttributeID = mapping.AttributeID
	}
	if projection.Validate() != nil {
		return unsupportedMapping(mapping)
	}
	write.PIIMappings = append(write.PIIMappings, projection)
	return orders.Validation{}
}

func resolveCustomerProjection(ctx context.Context, write *orders.CustomerOrderWrite, posting orders.Posting, reader orders.CustomerReader) []orders.Validation {
	if !write.HasPIIMappings() {
		return nil
	}
	if reader == nil {
		return []orders.Validation{{Field: "customer", Code: "protected_customer_projection_unavailable"}}
	}
	customer, err := reader.Customer(ctx, posting.Scope, posting.PostingNumber, posting.Customer)
	if err != nil || customer == nil {
		return []orders.Validation{{Field: "customer", Code: "protected_customer_projection_unavailable"}}
	}
	if err := write.ApplyCustomerProjection(*customer); err != nil {
		var invalid *orders.ShipmentAddressValueError
		if errors.As(err, &invalid) {
			return []orders.Validation{{Field: "customer.address." + invalid.Field, Code: invalid.Code}}
		}
		return []orders.Validation{{Field: "customer.address", Code: "protected_customer_projection_invalid_or_too_long"}}
	}
	return nil
}

func postingText(posting orders.Posting, source string) (orders.Field[string], bool) {
	switch strings.TrimPrefix(source, "posting.") {
	case "posting_number", "number":
		return orders.Present(posting.PostingNumber), true
	case "commercial_order_id", "order_id":
		return orders.Present(posting.CommercialOrderID), true
	case "order_number":
		return posting.OrderNumber, true
	case "parent_posting_number":
		return posting.ParentPostingNumber, true
	case "child_posting_numbers":
		if len(posting.ChildPostingNumbers) == 0 {
			return orders.Field[string]{Presence: orders.PresenceEmpty}, true
		}
		return orders.Present(strings.Join(posting.ChildPostingNumbers, ", ")), true
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
	}
	if strings.HasPrefix(source, "financial.") {
		return financialText(posting.Financial, strings.TrimPrefix(source, "financial."))
	}
	if strings.HasPrefix(source, "analytics.") {
		return analyticsText(posting.Analytics, strings.TrimPrefix(source, "analytics."))
	}
	if strings.HasPrefix(source, "delivery.") {
		return deliveryText(posting.Delivery, strings.TrimPrefix(source, "delivery."))
	}
	return orders.Field[string]{}, false
}

func financialText(financial orders.FinancialFacts, field string) (orders.Field[string], bool) {
	switch field {
	case "currency", "currency_code":
		return financial.Currency, true
	case "order_total":
		return moneyText(financial.OrderTotal), true
	case "order_total_amount":
		return decimalText(financial.OrderTotalAmount), true
	case "seller_discount":
		return moneyText(financial.SellerDiscount), true
	case "marketplace_discount":
		return moneyText(financial.MarketplaceDiscount), true
	case "commission":
		return moneyText(financial.Commission), true
	case "payout":
		return moneyText(financial.Payout), true
	default:
		return orders.Field[string]{}, false
	}
}

func analyticsText(analytics orders.AnalyticsFacts, field string) (orders.Field[string], bool) {
	switch field {
	case "region":
		return analytics.Region, true
	case "city":
		return analytics.City, true
	case "delivery_type":
		return analytics.DeliveryType, true
	case "premium":
		return boolText(analytics.Premium), true
	default:
		return orders.Field[string]{}, false
	}
}

func deliveryText(delivery orders.DeliveryFacts, field string) (orders.Field[string], bool) {
	switch field {
	case "provider_name":
		return delivery.ProviderName, true
	case "provider_id":
		return delivery.ProviderID, true
	case "tpl_provider_id":
		return delivery.TPLProviderID, true
	case "integration_type":
		return delivery.IntegrationType, true
	case "tracking_number":
		return delivery.TrackingNumber, true
	case "destination_type":
		return delivery.DestinationType, true
	case "pickup_point_id":
		return delivery.PickupPointID, true
	case "express":
		return boolText(delivery.Express), true
	case "window_from":
		return timeText(delivery.WindowFrom), true
	case "window_to":
		return timeText(delivery.WindowTo), true
	default:
		return orders.Field[string]{}, false
	}
}

func moneyText(value orders.Field[orders.Money]) orders.Field[string] {
	if !value.IsKnown() {
		return orders.Field[string]{Presence: value.Presence}
	}
	return orders.Present(string(value.Value.Amount) + " " + value.Value.Currency)
}

func decimalText(value orders.Field[orders.Decimal]) orders.Field[string] {
	if !value.IsKnown() {
		return orders.Field[string]{Presence: value.Presence}
	}
	return orders.Present(string(*value.Value))
}

func boolText(value orders.Field[bool]) orders.Field[string] {
	if !value.IsKnown() {
		return orders.Field[string]{Presence: value.Presence}
	}
	return orders.Present(strconv.FormatBool(*value.Value))
}

func timeText(value orders.Field[time.Time]) orders.Field[string] {
	if !value.IsKnown() {
		return orders.Field[string]{Presence: value.Presence}
	}
	return orders.Present(value.Value.UTC().Format(time.RFC3339))
}

func postingState(posting orders.Posting, config orders.Config) string {
	if stateID, ok := mappedStateID(posting, config); ok {
		return stateID
	}
	return config.StateID
}

func mappedStateID(posting orders.Posting, config orders.Config) (string, bool) {
	if !posting.Status.IsKnown() {
		return "", false
	}
	for _, mapping := range config.StatusMappings {
		if mapping.Status == *posting.Status.Value && sameField(mapping.Substatus, posting.Substatus) {
			return mapping.StateID, true
		}
	}
	for _, mapping := range config.StatusMappings {
		if mapping.Status == *posting.Status.Value && mapping.Substatus.IsMissing() {
			return mapping.StateID, true
		}
	}
	return "", false
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

// Reserve evaluation follows scalar mappings so configuration ordering cannot
// turn an explicit applicable=true into an accidental draft reserve.
func applyConfiguredReserve(write *orders.CustomerOrderWrite, posting orders.Posting, review orders.ReviewContext) orders.Validation {
	for _, mapping := range review.Config.FieldMappings {
		if mapping.Target == "positions.reserve" && mapping.Source == orders.ReserveOpenQuantitySource {
			if permission, ok := review.SplitReserveTransfers[posting.PostingNumber]; ok {
				return orders.ApplyTransferredSplitOpenQuantityReserve(posting, write, review.PositionBaselines[posting.PostingNumber], permission)
			}
			if review.SplitReserveBlocked[posting.PostingNumber] {
				return orders.Validation{Field: "posting.lineage", Code: "split_reserve_transfer_pending"}
			}
			if permission, ok := review.SplitReservePermissions[posting.PostingNumber]; ok {
				return orders.ApplySplitOpenQuantityReserve(posting, write, review.PositionBaselines[posting.PostingNumber], permission)
			}
			if permission, ok := review.SplitRetainedReserves[posting.PostingNumber]; ok {
				return orders.ApplyRetainedSplitOpenQuantityReserve(posting, write, review.PositionBaselines[posting.PostingNumber], permission)
			}
			return orders.ApplyOpenQuantityReserve(posting, write, review.PositionBaselines[posting.PostingNumber])
		}
	}
	return orders.Validation{}
}
