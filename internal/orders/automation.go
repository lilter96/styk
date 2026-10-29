package orders

import (
	"slices"
	"time"
)

func validGrantField(field string) bool {
	return writableTarget(field) || field == "posting.lines" || field == "posting.reason_id"
}

// Allows requires an exact current grant revision, action and every specified
// field. Explicit zero, null and empty fields never escape the field allowlist.
func (grant AutomationGrant) Allows(command Command, now time.Time) bool {
	if !grant.Active(now, command.Scope, command.ConfigRevision) || command.Validate() != nil || command.AutomationGrantRevision != grant.Revision {
		return false
	}
	if !slices.Contains(grant.Actions, command.Action) {
		return false
	}
	for _, field := range command.Fields() {
		if !slices.Contains(grant.Fields, field) {
			return false
		}
	}
	return true
}

// Fields lists the mutable fields in the frozen intent. Required configured
// references are separately authorized by the config revision and dispatch guard.
func (command Command) Fields() []string {
	fields := make(map[string]bool)
	if command.CustomerOrder != nil {
		command.CustomerOrder.collectFields(fields)
	}
	if action := command.PostingAction; action != nil {
		fields["posting.lines"] = len(action.Lines) > 0
		fields["posting.reason_id"] = action.ReasonID.IsSpecified()
	}
	result := make([]string, 0, len(fields))
	for field, specified := range fields {
		if specified {
			result = append(result, field)
		}
	}
	slices.Sort(result)
	return result
}

func (write CustomerOrderWrite) collectFields(fields map[string]bool) {
	fields["name"], fields["description"] = write.Name.IsSpecified(), write.Description.IsSpecified()
	fields["moment"], fields["delivery_at"] = write.Moment.IsSpecified(), write.DeliveryAt.IsSpecified()
	fields["applicable"] = write.Applicable.IsSpecified()
	fields["vat_enabled"], fields["vat_included"] = write.VATEnabled.IsSpecified(), write.VATIncluded.IsSpecified()
	fields["state"], fields["attribute"] = write.StateID != "", len(write.Attributes) > 0
	for _, position := range write.Positions {
		fields["positions.quantity"] = true
		fields["positions.reserve"] = fields["positions.reserve"] || position.Reserve.IsSpecified()
		fields["positions.price"] = fields["positions.price"] || position.Price.IsSpecified()
		fields["positions.vat"] = fields["positions.vat"] || position.VAT.IsSpecified()
	}
}
