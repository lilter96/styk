package ordermoney

import (
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// PlanNativeRetry keeps the same canonical physical event. It authorizes only
// a new reviewed attempt after every prior attempt for that event is proved to
// have had no effect. Fresh source, target, policy and capacity checks still run.
func PlanNativeRetry(c orders.Command, f orders.NativeConsumptionFacts, previousID string) (*orders.NativeRetryAuthorization, orders.Validation) {
	h, issue := readNativeConsumption(c, f)
	if issue.Code != "" {
		return nil, issue
	}
	return h.retryAuthorization(c, f.CheckedAt, previousID)
}

func (h *nativeConsumptionHistory) requestRetry(c orders.Command) orders.Validation {
	retry := c.NativeMovement.Retry
	if retry == nil {
		if h.eventAlreadyRecorded(c.NativeMovement) {
			return consumptionIssue("native_consumption_event_already_recorded")
		}
		return orders.Validation{}
	}
	expected, issue := h.retryAuthorization(c, h.checkedAt, retry.PreviousWorkID)
	if issue.Code != "" {
		return issue
	}
	if retry.Validate(c) != nil || *expected != *retry {
		return consumptionIssue("native_retry_authorization_changed")
	}
	return orders.Validation{}
}

func (h *nativeConsumptionHistory) retryAuthorization(c orders.Command, checkedAt time.Time, previousID string) (*orders.NativeRetryAuthorization, orders.Validation) {
	var latest orders.NativeConsumptionEvent
	for _, event := range h.works {
		m := event.Work.Command.NativeMovement
		if m.Correlation.Kind != c.NativeMovement.Correlation.Kind || m.Event.Digest != c.NativeMovement.Event.Digest {
			continue
		}
		if event.CreatedAt.IsZero() || event.CreatedAt.After(checkedAt) || !nativeConsumptionNoEffect(event) || !nativeRetryTargetMatches(c, event.Work.Command) {
			return nil, consumptionIssue("native_retry_prior_effect_or_target_unverified")
		}
		if latest.Work.ID == "" || event.CreatedAt.After(latest.CreatedAt) || event.CreatedAt.Equal(latest.CreatedAt) && event.Work.ID > latest.Work.ID {
			latest = event
		}
	}
	if latest.Work.ID == "" || latest.Work.ID != previousID || latest.Work.State != orders.StateRejected {
		return nil, consumptionIssue("native_retry_latest_rejection_required")
	}
	retry := &orders.NativeRetryAuthorization{PreviousWorkID: latest.Work.ID, PreviousCommandFingerprint: latest.Work.CommandFingerprint, EventDigest: c.NativeMovement.Event.Digest}
	if retry.Validate(c) != nil {
		return nil, consumptionIssue("native_retry_authorization_invalid")
	}
	return retry, orders.Validation{}
}

func nativeRetryTargetMatches(current, previous orders.Command) bool {
	a, b := current.NativeMovement, previous.NativeMovement
	if a == nil || b == nil || current.Scope != previous.Scope || current.Correlation != previous.Correlation || current.CommercialOrderID != previous.CommercialOrderID || current.Action != previous.Action || a.Correlation != b.Correlation || current.Warehouse.OzonWarehouseID != previous.Warehouse.OzonWarehouseID || current.Warehouse.MoyskladStoreID != previous.Warehouse.MoyskladStoreID {
		return false
	}
	if a.OrganizationID != b.OrganizationID || a.AgentID != b.AgentID || a.StoreID != b.StoreID || a.CurrencyID != b.CurrencyID || a.Event.Kind != b.Event.Kind || a.Event.Confirmed != b.Event.Confirmed || !nativeRetryAppliedMatches(a, b) || a.DemandWorkID != b.DemandWorkID {
		return false
	}
	if current.Action == orders.ActionSalesReturnCreate && (a.DemandBasis == nil || b.DemandBasis == nil || a.DemandBasis.Correlation != b.DemandBasis.Correlation) {
		return false
	}
	return nativeRetryLinesMatch(a.Lines, b.Lines)
}

func nativeRetryAppliedMatches(a, b *orders.NativeMovement) bool {
	if a.Applicable.Validate() != nil || b.Applicable.Validate() != nil || !a.Applicable.IsKnown() || !b.Applicable.IsKnown() || *a.Applicable.Value != *b.Applicable.Value {
		return false
	}
	return !*a.Applicable.Value || a.Event.OccurredAt.Equal(b.Event.OccurredAt)
}

func nativeRetryLinesMatch(current, previous []orders.NativeMovementLine) bool {
	if len(current) == 0 || len(current) != len(previous) {
		return false
	}
	seen := make(map[string]bool, len(current))
	for _, line := range current {
		if seen[line.OrderPositionID] || !nativeRetryLineMatches(line, previous) {
			return false
		}
		seen[line.OrderPositionID] = true
	}
	return true
}

func nativeRetryLineMatches(current orders.NativeMovementLine, previous []orders.NativeMovementLine) bool {
	for _, old := range previous {
		if current.OrderPositionID != old.OrderPositionID {
			continue
		}
		a, b := current.Mapping, old.Mapping
		return current.Identity == old.Identity && current.BasisPositionID == old.BasisPositionID && a.Scope == b.Scope && a.OzonProductID == b.OzonProductID && a.MoyskladKind == b.MoyskladKind && a.MoyskladID == b.MoyskladID
	}
	return false
}
