package orderinventory

import (
	"math"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/orders"
)

// Assess is a bounded attribution guard, not a second stock formula. Unknown
// quantities, partial reserve and incomplete lineage block the affected pool.
func Assess(input Input) Guard {
	guard := Guard{Obligations: make([]Obligation, 0, len(input.Observations)), Truncated: input.Truncated, Blocked: input.Truncated}
	excluded, lineageOK := reservationFrontier(input.Observations)
	seen := make(map[string]bool, len(input.Observations))
	var matched int64
	for _, observation := range input.Observations {
		if excluded[observationKey(observation.Posting)] {
			continue
		}
		obligation := assessObservation(input, observation)
		matched, obligation = uniqueAttribution(matched, obligation, seen)
		guard.Blocked = guard.Blocked || !settled(obligation.State)
		guard.Obligations = append(guard.Obligations, obligation)
	}
	if !lineageOK || !reserveFitsBaseline(input, matched) {
		invalidateAttribution(&guard)
	}
	return guard
}

func assessObservation(input Input, observation Observation) Obligation {
	posting := observation.Posting
	result := Obligation{Scope: posting.Scope, PostingNumber: posting.PostingNumber, CustomerOrderID: observation.Binding.CustomerOrderID, State: SourceObserved}
	quantity, ok := obligationUnits(observation)
	if !ok || posting.Scope.Tenant != input.Pool.Tenant || posting.Scope.MoyskladAccount != input.Pool.MoyskladAccount || posting.Evidence.Origin != orders.OriginOzon || observation.Quarantined || observation.Truncated || len(posting.Drift) != 0 {
		return pending(result, Unknown, "source_evidence_incomplete")
	}
	result.Quantity = strconv.FormatInt(quantity, 10)
	if !readbackProven(observation) {
		return pending(result, ProjectionPending, "native_readback_not_reconciled")
	}
	reserve, shipped, ok := nativeUnits(input, observation)
	if !ok {
		return pending(result, Unknown, "native_positions_incomplete")
	}
	result.Reserve = strconv.FormatInt(reserve, 10)
	if !baselineOrdered(input, observation) {
		return pending(result, ReserveObserved, "baseline_inclusion_unproven")
	}
	return classifyObligation(result, posting, quantity, reserve, shipped)
}

func classifyObligation(result Obligation, posting orders.Posting, quantity, reserve, shipped int64) Obligation {
	if quantity == 0 || knownStatus(posting, "cancelled") {
		result.State = Cancelled
		return result
	}
	if reserve == 0 && shipped == quantity && quantity > 0 {
		result.State = Shipped
		return result
	}
	if shipped == 0 && reserve == quantity && quantity > 0 {
		result.State = BaselineMatched
		return result
	}
	return pending(result, Unreflected, "obligation_not_fully_reserved")
}

func readbackProven(observation Observation) bool {
	work, target, binding := observation.Work, observation.Target, observation.Binding
	if work == nil || target == nil || work.State != orders.StateReconciled || !work.Command.Action.IsMirror() || !target.Complete || target.Revision == "" {
		return false
	}
	if binding.CustomerOrderID == "" || target.Scope != observation.Posting.Scope || binding.Scope != target.Scope || target.Correlation != binding || work.Scope != target.Scope {
		return false
	}
	if work.Command.ExpectedSourceRevision != observation.Posting.Evidence.Revision || work.Command.PostingNumber != binding.PostingNumber || binding.PostingNumber != observation.Posting.PostingNumber || work.Command.Correlation.SyncID != binding.SyncID || work.Command.Correlation.Marker != binding.Marker {
		return false
	}
	return !target.ObservedAt.IsZero() && !target.ObservedAt.Before(observation.Posting.Evidence.ObservedAt)
}

func baselineOrdered(input Input, observation Observation) bool {
	source, target := input.Baseline, observation.Target
	if !source.ObservationFound || source.ImportID == nil || *source.ImportID <= 0 || !source.TraversalComplete || source.Physical == nil || source.Reserve == nil || target == nil {
		return false
	}
	if source.Tenant != input.Pool.Tenant || source.Account != input.Pool.MoyskladAccount || source.WarehouseID != input.Pool.WarehouseID || source.ItemKind != input.Pool.ItemKind || source.ItemID != input.Pool.ItemID {
		return false
	}
	freshness := commitment.SourceFreshnessTime(source, input.Now)
	if freshness == nil || freshness.After(input.Now) || input.MaxAge <= 0 || input.Now.Sub(*freshness) >= input.MaxAge {
		return false
	}
	if observation.NativeStartedAt.IsZero() || observation.NativeStartedAt.Before(target.ObservedAt) || observation.NativeFinishedAt.Before(observation.NativeStartedAt) || input.BaselineStartedAt.Before(observation.NativeFinishedAt) || input.BaselineStartedAt.After(input.Now) {
		return false
	}
	_, physicalOK := whole(*source.Physical)
	return physicalOK
}

func obligationUnits(observation Observation) (int64, bool) {
	var total int64
	found := make(map[string]bool, len(observation.Lines))
	for _, line := range observation.Posting.Lines {
		if !hasLine(observation.Lines, line.Identity) {
			continue
		}
		quantity, ok := whole(string(line.Quantity))
		if !ok || found[line.Identity.Key()] {
			return 0, false
		}
		if line.CancelledQuantity.IsKnown() && line.CancelledQuantity.Value != nil {
			cancelled, valid := whole(string(*line.CancelledQuantity.Value))
			if !valid || cancelled > quantity {
				return 0, false
			}
			quantity -= cancelled
		}
		if total > math.MaxInt64-quantity {
			return 0, false
		}
		total += quantity
		found[line.Identity.Key()] = true
	}
	return total, len(found) > 0 && len(found) == len(observation.Lines)
}

func uniqueAttribution(total int64, result Obligation, seen map[string]bool) (int64, Obligation) {
	if !settled(result.State) {
		return total, result
	}
	key := result.Scope.Tenant + "/" + result.Scope.MoyskladAccount + "/" + result.CustomerOrderID
	reserve, ok := whole(result.Reserve)
	if !ok || seen[key] || total > math.MaxInt64-reserve {
		return total, pending(result, Unknown, "native_origin_not_unique")
	}
	seen[key] = true
	return total + reserve, result
}

func reserveFitsBaseline(input Input, matched int64) bool {
	if matched == 0 {
		return true
	}
	if input.Baseline.Reserve == nil {
		return false
	}
	reserve, ok := whole(*input.Baseline.Reserve)
	return ok && matched <= reserve
}

func invalidateAttribution(guard *Guard) {
	guard.Blocked = true
	for index := range guard.Obligations {
		if settled(guard.Obligations[index].State) {
			guard.Obligations[index] = pending(guard.Obligations[index], Unknown, "pool_reserve_or_split_attribution_unproven")
		}
	}
}

func whole(value string) (int64, bool) {
	if orders.Decimal(value).Validate() != nil || strings.HasPrefix(value, "-") {
		return 0, false
	}
	parts := strings.SplitN(value, ".", 2)
	if len(parts) == 2 && strings.Trim(parts[1], "0") != "" {
		return 0, false
	}
	number, err := strconv.ParseInt(parts[0], 10, 64)
	return number, err == nil
}

func settled(state State) bool {
	return state == BaselineMatched || state == Shipped || state == Cancelled
}

func pending(result Obligation, state State, reason string) Obligation {
	result.State, result.Reason = state, reason
	return result
}

func knownStatus(posting orders.Posting, value string) bool {
	return posting.Status.IsKnown() && posting.Status.Value != nil && *posting.Status.Value == value
}

func hasLine(lines []orders.LineIdentity, wanted orders.LineIdentity) bool {
	for _, line := range lines {
		if line == wanted {
			return true
		}
	}
	return false
}
