package orders

import (
	"slices"
	"time"
)

// This is the observed vocabulary already supported by the posting/split
// contracts, not a status-to-movement mapping. Physical evidence stays separate.
func AutomaticNativeSourceSupported(posting Posting) bool {
	return posting.Status.IsKnown() && slices.Contains([]string{"awaiting_registration", "awaiting_approve", "awaiting_packaging", "awaiting_deliver", "delivering", "driver_pickup", "delivered", "cancelled", SupersededSplitParentStatus, "acceptance_in_progress", "arbitration", "client_arbitration", "not_accepted"}, *posting.Status.Value)
}

// AllowsNative binds a physical seller declaration to its exact native order
// transition. Packing and marketplace status observations never satisfy it.
func (policy LifecyclePolicy) AllowsNative(command Command, now time.Time) bool {
	intent, movement := command.MerchantIntent, command.NativeMovement
	if intent == nil || movement == nil || intent.Validate() != nil || !command.Action.IsNativeMovement() || !policy.Active(now, command.Scope, command.ConfigRevision) {
		return false
	}
	if command.LifecyclePolicyRevision != policy.Revision || command.AutomationGrantRevision != 0 || intent.Scope != command.Scope || intent.Correlation != command.Correlation || intent.Action != command.Action || intent.ConfigRevision != command.ConfigRevision || intent.CredentialRevision != command.Credentials.Moysklad || intent.AfterRevision != command.ExpectedTargetRevision {
		return false
	}
	if _, found := policy.Mapping(*intent); !found || !intent.WholePosting || now.Before(intent.ObservedAt) || now.Sub(intent.ObservedAt) > 15*time.Minute {
		return false
	}
	occurred, err := NativeParentVersionTime(intent.AfterUpdatedAt)
	if err != nil || !movement.Event.OccurredAt.Equal(occurred) || !movement.Applicable.IsKnown() || !*movement.Applicable.Value || !movement.Event.Confirmed {
		return false
	}
	if command.Action == ActionDemandCreate {
		return policy.AutoDemand && policy.DemandMoment == OnHandoffConfirmed && intent.EventKind == "handoff_confirmed" && movement.Event.Kind == "shipment_dispatched"
	}
	return policy.AutoReturn && intent.EventKind == "return_received" && movement.Event.Kind == "return_received" && movement.DemandBasis != nil && movement.DemandWorkID != ""
}

// AllowedByLifecycle preserves the native quantity, freshness and parent guards.
// Only the separate current lifecycle consent replaces the reviewed 24h grant;
// it never expands an existing mirror grant or invents a physical event.
func (movement NativeMovement) AllowedByLifecycle(config Config, command Command, now time.Time) bool {
	policy := config.NativeDocuments
	if config.Scope != command.Scope || config.Revision != command.ConfigRevision || config.Lifecycle == nil || policy == nil || !policy.Enabled || policy.Validate() != nil || !slices.Contains(policy.Actions, command.Action) || !config.Lifecycle.AllowsNative(command, now) {
		return false
	}
	// Unposted parent counters do not expose a seller-created linked demand.
	// Keep that reviewed-only profile until linked-document coverage is proved.
	if movement.ParentEffect == NativeParentUnpostedDocument || !movement.ParentApplicable.IsKnown() || !*movement.ParentApplicable.Value {
		return false
	}
	if movement.Event.OccurredAt.After(now) || now.Sub(movement.Event.OccurredAt) > time.Duration(policy.EventMaxAgeSeconds)*time.Second || len(movement.Lines) == 0 {
		return false
	}
	if command.Action == ActionDemandCreate && movement.DemandCoverageValidation(command, now).Code != "" {
		return false
	}
	for _, line := range movement.Lines {
		if !positiveDecimal(line.Quantity) || !nonnegativeNotGreater(line.Quantity, policy.MaximumQuantity) {
			return false
		}
	}
	return true
}

// AutomaticDemandCoverage cannot adopt a foreign draft by name or by zero
// shipped counters. Known existing links require explicit operator review.
func AutomaticDemandCoverage(links Field[[]string]) Validation {
	if links.Validate() != nil || links.IsMissing() || links.Presence == PresenceNull {
		return Validation{Field: "customer_order.demands", Code: "native_linked_demand_coverage_unavailable"}
	}
	if links.Presence != PresenceEmpty {
		return Validation{Field: "customer_order.demands", Code: "native_existing_linked_demand_requires_review"}
	}
	return Validation{}
}
