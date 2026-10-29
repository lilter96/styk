package postgres

import (
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func orderReadbackMatches(command orders.Command, observed orders.TargetObservation) bool {
	if command.CustomerOrder == nil || !observed.Complete || observed.Scope != command.Scope || observed.Revision == "" || observed.ObservedAt.IsZero() {
		return false
	}
	correlation := observed.Correlation
	if correlation.Validate() != nil || correlation.Scope != command.Scope || correlation.PostingNumber != command.PostingNumber || correlation.SyncID != command.Correlation.SyncID || correlation.Marker != command.Correlation.Marker || correlation.CustomerOrderID == "" {
		return false
	}
	if command.Correlation.CustomerOrderID != "" && command.Correlation.CustomerOrderID != correlation.CustomerOrderID {
		return false
	}
	wanted := *command.CustomerOrder
	if !orderReadbackFields(wanted, observed.Fields) || len(wanted.Positions) != len(observed.Positions) {
		return false
	}
	return orderReadbackPositions(wanted.Positions, observed.Positions)
}

func orderReadbackFields(wanted, actual orders.CustomerOrderWrite) bool {
	if wanted.OrganizationID != actual.OrganizationID || wanted.AgentID != actual.AgentID || wanted.StoreID != actual.StoreID || wanted.StateID != actual.StateID || wanted.SalesChannelID != actual.SalesChannelID {
		return false
	}
	if !orderFieldEqual(wanted.Name, actual.Name) || !orderFieldEqual(wanted.Description, actual.Description) || !orderFieldEqual(wanted.Applicable, actual.Applicable) || !orderFieldEqual(wanted.VATEnabled, actual.VATEnabled) || !orderFieldEqual(wanted.VATIncluded, actual.VATIncluded) {
		return false
	}
	if !orderTimeFieldEqual(wanted.Moment, actual.Moment) || !orderTimeFieldEqual(wanted.DeliveryAt, actual.DeliveryAt) {
		return false
	}
	attributes := make(map[string]orders.Field[string], len(actual.Attributes))
	for _, attribute := range actual.Attributes {
		if _, exists := attributes[attribute.AttributeID]; exists {
			return false
		}
		attributes[attribute.AttributeID] = attribute.Value
	}
	for _, attribute := range wanted.Attributes {
		if !orderFieldEqual(attribute.Value, attributes[attribute.AttributeID]) {
			return false
		}
	}
	return true
}

func orderReadbackPositions(wanted []orders.PositionWrite, actual []orders.TargetPosition) bool {
	used := make(map[int]bool, len(actual))
	for _, position := range wanted {
		index := orderReadbackPositionIndex(position, wanted, actual)
		if index < 0 || used[index] || !orderPositionValuesEqual(position, actual[index]) {
			return false
		}
		used[index] = true
	}
	return true
}

func orderReadbackPositionIndex(wanted orders.PositionWrite, all []orders.PositionWrite, actual []orders.TargetPosition) int {
	result := -1
	for index, candidate := range actual {
		if candidate.MoyskladKind != wanted.Mapping.MoyskladKind || candidate.MoyskladID != wanted.Mapping.MoyskladID {
			continue
		}
		if candidate.Identity.Validate() == nil {
			if candidate.Identity.Key() != wanted.Identity.Key() {
				continue
			}
		} else if !orderUniqueAssortment(wanted, all) {
			return -1
		}
		if result >= 0 {
			return -1
		}
		result = index
	}
	return result
}

func orderUniqueAssortment(wanted orders.PositionWrite, all []orders.PositionWrite) bool {
	count := 0
	for _, candidate := range all {
		if candidate.Mapping.MoyskladKind == wanted.Mapping.MoyskladKind && candidate.Mapping.MoyskladID == wanted.Mapping.MoyskladID {
			count++
		}
	}
	return count == 1
}

func orderPositionValuesEqual(wanted orders.PositionWrite, actual orders.TargetPosition) bool {
	return actual.ID != "" && orderDecimalEqual(wanted.Quantity, actual.Quantity) && orderDecimalFieldEqual(wanted.Reserve, actual.Reserve) && orderMoneyFieldEqual(wanted.Price, actual.Price) && orderDecimalFieldEqual(wanted.VAT, actual.VAT)
}

func orderFieldEqual[T comparable](wanted, actual orders.Field[T]) bool {
	if !wanted.IsKnown() {
		return true
	}
	return actual.IsKnown() && wanted.Value != nil && actual.Value != nil && *wanted.Value == *actual.Value
}

func orderTimeFieldEqual(wanted, actual orders.Field[time.Time]) bool {
	if !wanted.IsKnown() {
		return true
	}
	return actual.IsKnown() && wanted.Value != nil && actual.Value != nil && wanted.Value.Equal(*actual.Value)
}

func orderDecimalFieldEqual(wanted, actual orders.Field[orders.Decimal]) bool {
	if !wanted.IsKnown() {
		return true
	}
	return actual.IsKnown() && wanted.Value != nil && actual.Value != nil && orderDecimalEqual(*wanted.Value, *actual.Value)
}

func orderMoneyFieldEqual(wanted, actual orders.Field[orders.Money]) bool {
	if !wanted.IsKnown() {
		return true
	}
	return actual.IsKnown() && wanted.Value != nil && actual.Value != nil && wanted.Value.Currency == actual.Value.Currency && orderDecimalEqual(wanted.Value.Amount, actual.Value.Amount)
}

func orderDecimalEqual(before, after orders.Decimal) bool {
	if before.Validate() != nil || after.Validate() != nil {
		return false
	}
	return normalizedOrderDecimal(before) == normalizedOrderDecimal(after)
}

func normalizedOrderDecimal(value orders.Decimal) string {
	text := string(value)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		return "0"
	}
	return text
}

func sanitizedOrderTarget(command orders.Command, target orders.TargetObservation) orders.TargetObservation {
	if command.CustomerOrder != nil {
		target.Fields = *command.CustomerOrder
	}
	return target
}
