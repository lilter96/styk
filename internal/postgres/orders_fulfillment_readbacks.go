package postgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Fulfillment facts join normal ingestion in the same completion transaction.
// This records point observations, never a complete polling window/checkpoint.
func commitFulfillmentReadbackFacts(ctx context.Context, tx pgx.Tx, work orders.Work, completion orders.Completion, now time.Time) error {
	if completion.State != orders.StateReconciled || work.Command.Action.IsMirror() || work.Command.Action.IsNativeMovement() {
		return nil
	}
	details := []orders.Posting{}
	if work.Command.Action == orders.ActionPostingSplit {
		details = slices.Clone(completion.Receipt.ObservedPostings)
	} else if completion.ObservedPosting != nil {
		details = append(details, *completion.ObservedPosting)
	}
	if len(details) == 0 {
		return nil
	}
	sequence, idle, err := fulfillmentReadbackSequence(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	for _, posting := range details {
		if err := insertFulfillmentReadback(ctx, tx, work, posting, sequence, idle, now); err != nil {
			return err
		}
	}
	return nil
}

func insertFulfillmentReadback(ctx context.Context, tx pgx.Tx, work orders.Work, posting orders.Posting, sequence int64, idle bool, now time.Time) error {
	if posting.Scope != work.Scope || posting.CommercialOrderID != work.Command.CommercialOrderID {
		return orders.ErrStale
	}
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	if checked.IsZero() || checked.After(now) || posting.Evidence.ObservedAt.After(now) {
		return orders.ErrStale
	}
	serial, err := fulfillmentReadbackSerial(ctx, tx, work, posting, idle)
	if err != nil {
		return err
	}
	kind := orders.CollectionSnapshot
	if serial {
		kind = orders.CollectionSerialDetail
	}
	posting = orderSequencedObservation(posting, sequence, kind)
	posting.PII = nil
	if _, err = insertTimedOrderPosting(ctx, tx, posting, orderObservationClock{RecordedAt: now, CheckedAt: checked}); err != nil {
		return err
	}
	return nil
}

func fulfillmentReadbackSequence(ctx context.Context, tx pgx.Tx, scope orders.Scope) (int64, bool, error) {
	var idle bool
	err := tx.QueryRow(ctx, `SELECT token IS NULL OR lease_until<=clock_timestamp() FROM orders_ingestion_leases WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 FOR UPDATE`, ordersScopeArgs(scope)...).Scan(&idle)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, ordersError("lock readback observation admission", err)
	}
	sequence, err := nextOrderObservationSequence(ctx, tx, scope)
	return sequence, idle, ordersRowsError("sequence fulfillment point observation", err)
}

func fulfillmentReadbackSerial(ctx context.Context, tx pgx.Tx, work orders.Work, posting orders.Posting, idle bool) (bool, error) {
	if !idle || posting.PostingNumber != work.Command.PostingNumber {
		return false, nil
	}
	current, err := readOrderPosting(ctx, tx, posting.Scope, posting.PostingNumber)
	if errors.Is(err, orders.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.Evidence.Revision == work.Command.ExpectedSourceRevision, nil
}
