package orderflow

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) readback(ctx context.Context, work orders.Work) error {
	allowed, err := r.store.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Now: r.now()})
	if err != nil {
		return r.deferReadback(ctx, work, "READBACK_AUTHORITY_UNAVAILABLE")
	}
	if !allowed {
		return r.deferReadback(ctx, work, "READBACK_AUTHORITY_CHANGED")
	}
	decision, sourceErr := r.reviewedDecision(ctx, work)
	if sourceErr == nil {
		sourceErr = r.freshSource(ctx, work, decision.Posting)
	}
	target, err := r.readWorkTarget(ctx, work)
	if err != nil {
		return r.deferReadback(ctx, work, "TARGET_READBACK_PENDING")
	}
	if sourceErr != nil {
		return r.deferReadback(ctx, work, "SOURCE_CHANGED_REQUIRES_REVIEW")
	}
	if !targetRepresents(work.Command, target) {
		return r.deferReadback(ctx, work, "TARGET_FACTS_NOT_CONFIRMED")
	}
	return r.reconcileTarget(ctx, work, target)
}

func (r *Runner) readWorkTarget(ctx context.Context, work orders.Work) (orders.TargetObservation, error) {
	if !work.Command.Action.IsMirror() {
		return orders.TargetObservation{}, orders.ErrBlocked
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	correlation, err := client.ReadOrderCorrelation(ctx, work.Command.Correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return orders.TargetObservation{}, orders.ErrBlocked
	}
	target, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	if !validTarget(work.Command.Correlation, target) {
		return orders.TargetObservation{}, orders.ErrStale
	}
	if work.Receipt.ExternalID != "" && target.Correlation.CustomerOrderID != work.Receipt.ExternalID {
		return orders.TargetObservation{}, orders.ErrConflict
	}
	return target, nil
}

func (r *Runner) reconcileTarget(ctx context.Context, work orders.Work, target orders.TargetObservation) error {
	correlation := target.Correlation
	return r.complete(ctx, work, orders.Completion{State: orders.StateReconciled, Code: "TARGET_FACTS_CONFIRMED", Correlation: &correlation, ObservedTarget: &target})
}

func (r *Runner) deferReadback(ctx context.Context, work orders.Work, code string) error {
	state := orders.StatePolling
	if work.State == orders.StateUnknown || code == "SOURCE_CHANGED_REQUIRES_REVIEW" {
		state = orders.StateUnknown
	}
	return r.complete(ctx, work, orders.Completion{State: state, Code: code, NextRunAt: r.now().Add(r.options.PollInterval)})
}

func sameTargetSnapshot(before, after orders.TargetObservation) bool {
	left, err := json.Marshal(canonicalTarget(before))
	if err != nil {
		return false
	}
	right, err := json.Marshal(canonicalTarget(after))
	return err == nil && string(left) == string(right)
}

func canonicalTarget(target orders.TargetObservation) orders.TargetObservation {
	target.ObservedAt = time.Time{}
	target.Fields.OwnedFields = nil
	target.Fields.Positions = nil
	target.Fields.Attributes = slices.Clone(target.Fields.Attributes)
	slices.SortFunc(target.Fields.Attributes, func(left, right orders.AttributeWrite) int {
		return strings.Compare(left.AttributeID, right.AttributeID)
	})
	target.Positions = slices.Clone(target.Positions)
	slices.SortFunc(target.Positions, func(left, right orders.TargetPosition) int { return strings.Compare(left.ID, right.ID) })
	return target
}
