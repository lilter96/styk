package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
	"time"
)

// One set-based read per selected batch; no provider I/O or network transaction.
// Only immutable successful reconciliation evidence can establish line ownership.
func enrichOrderPositionBaselines(ctx context.Context, q ordersQuerier, result *orders.ReviewContext) error {
	numbers := make([]string, 0, len(result.Postings))
	for _, posting := range result.Postings {
		numbers = append(numbers, posting.PostingNumber)
	}
	var now time.Time
	if err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ordersError("read position ownership expiry clock", err)
	}
	rows, err := q.Query(ctx, positionOwnershipSQL, result.Config.Scope.Tenant, result.Config.Scope.MoyskladAccount, result.Config.Scope.OzonAccount, numbers)
	if err != nil {
		return ordersError("read immutable position frontiers", err)
	}
	defer rows.Close()
	result.PositionBaselines = make(map[string]orders.PositionBaseline, len(numbers))
	for rows.Next() {
		number, baseline, readErr := readPositionOwnership(rows, result.Config.Scope, now)
		if readErr != nil {
			return readErr
		}
		if baseline != nil {
			result.PositionBaselines[number] = *baseline
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ordersError("read immutable position frontiers", err)
	}
	return enrichRecoveryPositionBaselines(ctx, q, result, numbers)
}

func validateOrderPositionBaseline(command orders.Command, current orders.ReviewContext) error {
	if command.Action == orders.ActionMirrorCompositionUpdate {
		baseline, found := current.PositionBaselines[command.PostingNumber]
		if !found || command.PositionReplacement == nil || !baseline.Equal(command.PositionReplacement.Baseline) {
			return orders.ErrStale
		}
		for _, binding := range baseline.Bindings {
			if !orderProductMappingPresent(binding.Source.Mapping, current.ProductMappings) {
				return orders.ErrStale
			}
		}
		return command.PositionReplacement.Validate(command)
	}
	if command.PositionBaseline != nil || command.WritesApplication() {
		baseline, found := current.PositionBaselines[command.PostingNumber]
		if !found || command.PositionBaseline == nil || !baseline.Equal(*command.PositionBaseline) {
			return orders.ErrStale
		}
		return command.ValidateApplicationBaseline()
	}
	if command.Action != orders.ActionMirrorPositionUpdate {
		return nil
	}
	if command.PositionPatch == nil {
		return orders.ErrInvalid
	}
	baseline, found := current.PositionBaselines[command.PostingNumber]
	if !found || !baseline.Equal(command.PositionPatch.Baseline) {
		return orders.ErrStale
	}
	return nil
}

func readNativeOrMirrorBaseline(work orders.Work, target orders.TargetObservation, raw []byte, confirmed *time.Time) (orders.PositionBaseline, error) {
	if work.Command.Action.IsMirror() {
		return orders.ProjectionPositionBaseline(work, target)
	}
	if confirmed == nil || len(raw) == 0 {
		return orders.PositionBaseline{}, orders.ErrBlocked
	}
	var native orders.NativeDocumentObservation
	if err := ordersDecode(raw, &native); err != nil {
		return orders.PositionBaseline{}, err
	}
	if !ordermoney.NativeParentEffectObserved(work.Command, target) || ordermoney.NativeDocumentTotal(work.Command, native).Code != "" {
		return orders.PositionBaseline{}, orders.ErrBlocked
	}
	return orders.NativePositionBaseline(work, target, native, *confirmed)
}
