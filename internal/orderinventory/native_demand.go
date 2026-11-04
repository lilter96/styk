package orderinventory

import (
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
	"time"
)

func nativeDemandReadbackProven(observation Observation) bool {
	work, target, document := observation.Work, observation.Target, observation.NativeDocument
	if work == nil || target == nil || document == nil || work.Command.Action != orders.ActionDemandCreate || observation.NativeConfirmedAt.IsZero() {
		return false
	}
	_, err := orders.NativePositionBaseline(*work, *target, *document, observation.NativeConfirmedAt)
	return err == nil && ordermoney.NativeParentEffectObserved(work.Command, *target) && ordermoney.NativeDocumentTotal(work.Command, *document).Code == ""
}

func nativeDemandBaselineOrdered(input Input, observation Observation) bool {
	source := input.Baseline
	if !nativeDemandReadbackProven(observation) || !source.ObservationFound || !source.TraversalComplete || source.ImportID == nil || source.Physical == nil || source.Reserve == nil || observation.NativeConfirmedAt.After(input.Now) {
		return false
	}
	if source.Tenant != input.Pool.Tenant || source.Account != input.Pool.MoyskladAccount || source.WarehouseID != input.Pool.WarehouseID || source.ItemKind != input.Pool.ItemKind || source.ItemID != input.Pool.ItemID {
		return false
	}
	at := commitment.SourceFreshnessTime(source, input.Now)
	start := NativeEffectBaselineStart(input.Pool, source, input.BaselineStartedAt, input.Now)
	if at == nil || at.After(input.Now) || input.MaxAge <= 0 || input.Now.Sub(*at) >= input.MaxAge || !start.After(observation.NativeConfirmedAt) {
		return false
	}
	if observation.NativePointRecordedAt.IsZero() && observation.NativePointAdmittedAt.IsZero() {
		if observation.NativeStartedAt.IsZero() || !observation.NativeStartedAt.After(observation.NativeConfirmedAt) || observation.NativeFinishedAt.Before(observation.NativeStartedAt) || observation.NativeFinishedAt.After(input.Now) || !start.After(observation.NativeFinishedAt) {
			return false
		}
	} else if !nativePointBaselineOrdered(input, observation) {
		return false
	}
	if !nativeDemandProviderVersion(observation, input.Now) {
		return false
	}
	_, ok := whole(*source.Physical)
	return ok
}

// Equality is provider-version/counter evidence; the batch's client timestamps
// are additional conservative checks and are never called DB-clock admission.
func nativeDemandProviderVersion(observation Observation, now time.Time) bool {
	target := observation.Target
	if target == nil || !target.ProviderUpdatedAt.IsKnown() {
		return false
	}
	updated := *target.ProviderUpdatedAt.Value
	date, err := orders.NativeParentVersionTime(updated)
	if err != nil || date.After(now) {
		return false
	}
	for _, position := range observation.Positions {
		if position.SourceUpdated == nil || *position.SourceUpdated != updated {
			return false
		}
	}
	return len(observation.Positions) > 0
}

func nativeDemandUnits(input Input, observation Observation) (int64, int64, bool) {
	if observation.NativeSyncID == nil || *observation.NativeSyncID != observation.Binding.SyncID || !nativeDemandReadbackProven(observation) || !nativeDemandProviderVersion(observation, input.Now) {
		return 0, 0, false
	}
	baseline, err := orders.NativePositionBaseline(*observation.Work, *observation.Target, *observation.NativeDocument, observation.NativeConfirmedAt)
	if err != nil {
		return 0, 0, false
	}
	owned, full := nativeDemandTargets(input.Pool, baseline, *observation.Target)
	filtered := observation
	filtered.Positions = nil
	seen := make(map[string]bool, len(observation.Positions))
	for _, position := range observation.Positions {
		target, found := full[position.PositionID]
		if !found || seen[position.PositionID] || !nativePositionScoped(input, observation, position) {
			return 0, 0, false
		}
		if _, _, valid := positionUnits(position, target); !valid || position.Shipped == nil || !target.Shipped.IsKnown() || !orders.ExactDecimalEqual(orders.Decimal(*position.Shipped), *target.Shipped.Value) {
			return 0, 0, false
		}
		seen[position.PositionID] = true
		if _, ownedLine := owned[position.PositionID]; ownedLine {
			filtered.Positions = append(filtered.Positions, position)
		}
	}
	if len(seen) != len(full) {
		return 0, 0, false
	}
	for id := range owned {
		if _, found := full[id]; !found {
			delete(owned, id)
		}
	}
	return sumNativePositions(input, filtered, owned)
}

func nativeDemandProofBlocker(observation Observation) string {
	work := observation.Work
	if work == nil || work.Command.Action != orders.ActionDemandCreate {
		return ""
	}
	if work.State != orders.StateReconciled {
		return "native_movement_outcome_unresolved"
	}
	if observation.NativeConfirmedAt.IsZero() {
		return "native_db_confirmation_missing"
	}
	if observation.NativeDocument == nil || observation.Target == nil {
		return "native_document_readback_missing"
	}
	if !observation.Target.ProviderUpdatedAt.IsKnown() {
		return "native_parent_provider_version_missing"
	}
	if work.Command.NativeMovement == nil {
		return "native_movement_intent_missing"
	}
	original := work.Command.NativeMovement.SourceBaseline
	if original.RecoveryID != "" || original.EvidenceClass == "OPERATOR_ATTESTED" {
		return "native_origin_ownership_operator_attested"
	}
	if work.Command.ExpectedSourceRevision != observation.Posting.Evidence.Revision {
		return "native_source_revision_changed"
	}
	return ""
}

func nativeDemandOrderingBlocker(input Input, observation Observation) string {
	if observation.Work == nil || !observation.Work.Command.Action.IsNativeMovement() {
		return "baseline_inclusion_unproven"
	}
	if input.Baseline.FreshTraversal == nil {
		return "native_stock_db_traversal_required"
	}
	if observation.NativeStartedAt.IsZero() || observation.NativeFinishedAt.IsZero() {
		return "native_order_complete_traversal_required"
	}
	if !nativeDemandProviderVersion(observation, input.Now) {
		return "native_order_provider_version_unproven"
	}
	return "native_order_stock_check_ordering_unproven"
}

func nativeDemandTargets(pool domain.StockPool, baseline orders.PositionBaseline, target orders.TargetObservation) (map[string]orders.TargetPosition, map[string]orders.TargetPosition) {
	owned := make(map[string]orders.TargetPosition, len(baseline.Bindings))
	full := make(map[string]orders.TargetPosition, len(target.Positions))
	for _, binding := range baseline.Bindings {
		owned[binding.Native.ID] = binding.Native
	}
	for _, position := range target.Positions {
		if position.MoyskladKind == pool.ItemKind && position.MoyskladID == pool.ItemID {
			full[position.ID] = position
		}
	}
	return owned, full
}
