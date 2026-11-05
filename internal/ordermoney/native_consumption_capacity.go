package ordermoney

import (
	"sort"

	"github.com/lilter96/styk/internal/orders"
)

func (h *nativeConsumptionHistory) positions(c orders.Command) orders.Validation {
	for _, binding := range c.NativeMovement.SourceBaseline.Bindings {
		if _, found := h.demands[binding.Native.ID]; !found {
			h.demands[binding.Native.ID] = nil
		}
	}
	keys := make([]string, 0, len(h.demands))
	for id := range h.demands {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		total, err := nativeConsumedSum(h.demands[id])
		if err != nil {
			return consumptionIssue("native_consumption_quantity_invalid")
		}
		h.seal.Positions = append(h.seal.Positions, orders.NativeConsumedPosition{OrderPositionID: id, GrossDemand: total})
	}
	return orders.Validation{}
}

func nativeConsumedSum(values []orders.Decimal) (orders.Decimal, error) {
	total, err := parseDecimal("0")
	if err != nil {
		return "", err
	}
	for _, quantity := range values {
		value, parseErr := parseDecimal(quantity)
		if parseErr != nil || value.coefficient.Sign() < 0 {
			return "", orders.ErrInvalid
		}
		total = total.plus(value)
	}
	return total.decimal()
}

func (h *nativeConsumptionHistory) capacity(c orders.Command) orders.Validation {
	m := c.NativeMovement
	if m.SourceBaseline.Validate() != nil || m.SourceBaseline.Scope != c.Scope || m.SourceBaseline.Correlation != c.Correlation || m.Applicable.Validate() != nil || !m.Applicable.IsKnown() || len(m.Lines) == 0 {
		return consumptionIssue("native_consumption_request_unverified")
	}
	if m.ParentEffect != orders.NativeParentShippedCounter && m.ParentEffect != orders.NativeParentUnpostedDocument {
		return consumptionIssue("native_consumption_parent_mode_unsupported")
	}
	if m.ParentEffect == orders.NativeParentUnpostedDocument && !orders.NativeUnpostedParentSelection(c) {
		return consumptionIssue("native_consumption_zero_parent_unverified")
	}
	if h.eventAlreadyRecorded(m) {
		return consumptionIssue("native_consumption_event_already_recorded")
	}
	for _, line := range m.Lines {
		quantity, err := parseDecimal(line.Quantity)
		if err != nil || quantity.coefficient.Sign() <= 0 {
			return consumptionIssue("native_consumption_request_quantity_invalid")
		}
		var issue orders.Validation
		if c.Action == orders.ActionDemandCreate {
			issue = h.demandCapacity(c, line)
		} else {
			issue = h.returnCapacity(c, line)
		}
		if issue.Code != "" {
			return issue
		}
	}
	return orders.Validation{}
}

func (h *nativeConsumptionHistory) eventAlreadyRecorded(m *orders.NativeMovement) bool {
	for _, event := range h.works {
		previous := event.Work.Command.NativeMovement
		if previous != nil && previous.Correlation.Kind == m.Correlation.Kind && previous.Event.Digest == m.Event.Digest {
			return true
		}
	}
	return false
}

func (h *nativeConsumptionHistory) demandCapacity(c orders.Command, line orders.NativeMovementLine) orders.Validation {
	position, found := nativeConsumptionPosition(c.TargetPositions, line.OrderPositionID)
	if !found || !nativeConsumptionLineBound(c, line, position) || !h.identityUnchanged(line) {
		return consumptionIssue("native_consumption_position_identity_changed")
	}
	consumed := append([]orders.Decimal{}, h.demands[line.OrderPositionID]...)
	consumed = append(consumed, line.Quantity)
	if _, err := RemainingQuantity(position.Quantity, consumed...); err != nil {
		return consumptionIssue("native_consumption_demand_exceeds_order")
	}
	if !position.Shipped.IsKnown() {
		return consumptionIssue("native_consumption_parent_counter_unproven")
	}
	if _, err := RemainingQuantity(position.Quantity, *position.Shipped.Value, line.Quantity); err != nil {
		return consumptionIssue("native_consumption_demand_exceeds_counter_remaining")
	}
	return orders.Validation{}
}

func nativeConsumptionPosition(positions []orders.TargetPosition, id string) (orders.TargetPosition, bool) {
	var result orders.TargetPosition
	found := false
	for _, position := range positions {
		if position.ID == id {
			if found {
				return result, false
			}
			result, found = position, true
		}
	}
	return result, found
}

func nativeConsumptionLineBound(c orders.Command, line orders.NativeMovementLine, position orders.TargetPosition) bool {
	for _, binding := range c.NativeMovement.SourceBaseline.Bindings {
		if binding.Native.ID == line.OrderPositionID && binding.Source.Identity == line.Identity && binding.Source.Mapping.ID == line.Mapping.ID && binding.Source.Mapping.Version == line.Mapping.Version && binding.Source.Mapping.Scope == c.Scope && binding.Source.Mapping.MoyskladKind == line.Mapping.MoyskladKind && binding.Source.Mapping.MoyskladID == line.Mapping.MoyskladID {
			return orders.PositionRepresented(binding.Source, position)
		}
	}
	return false
}

func (h *nativeConsumptionHistory) identityUnchanged(line orders.NativeMovementLine) bool {
	for _, event := range h.works {
		m := event.Work.Command.NativeMovement
		if event.Work.Command.Action != orders.ActionDemandCreate || m == nil || !m.Applicable.IsKnown() || !*m.Applicable.Value || event.Work.State != orders.StateReconciled {
			continue
		}
		for _, old := range m.Lines {
			if old.OrderPositionID == line.OrderPositionID && (old.Identity != line.Identity || old.Mapping.MoyskladKind != line.Mapping.MoyskladKind || old.Mapping.MoyskladID != line.Mapping.MoyskladID) || old.Identity == line.Identity && old.OrderPositionID != line.OrderPositionID {
				return false
			}
		}
	}
	return true
}
