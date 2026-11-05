package postgres

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

// Demand history is gross consumption. A return, mapping revision, credential
// rotation or a later zero shipped counter never removes an earlier event.
func enrichNativeConsumption(ctx context.Context, q ordersQuerier, current *orders.ReviewContext) error {
	now, err := ordersClock(ctx, q)
	if err != nil {
		return err
	}
	selected := make(map[string]bool, len(current.Postings))
	for _, posting := range current.Postings {
		selected[posting.PostingNumber] = true
	}
	current.NativeConsumption = make(map[string]orders.NativeConsumptionFacts, len(selected))
	for _, correlation := range current.Correlations {
		if correlation.CustomerOrderID == "" || !selected[correlation.PostingNumber] {
			continue
		}
		facts, e := readNativeConsumption(ctx, q, correlation, current.RevalidationWorkID, current.RevalidationCommandFingerprint, now)
		if e != nil {
			return e
		}
		current.NativeConsumption[correlation.PostingNumber] = facts
	}
	return enrichNativeSplitConsumption(ctx, q, current, now)
}

func readNativeConsumption(ctx context.Context, q ordersQuerier, correlation orders.Correlation, selfID, fingerprint string, now time.Time) (orders.NativeConsumptionFacts, error) {
	facts := orders.NativeConsumptionFacts{Scope: correlation.Scope, Correlation: correlation, CheckedAt: now, Events: []orders.NativeConsumptionEvent{}}
	bound, err := nativeConsumptionBinding(ctx, q, correlation)
	if err != nil || !bound {
		return facts, err
	}
	facts.Complete = true
	args := append(ordersScopeArgs(correlation.Scope), correlation.PostingNumber, selfID, fingerprint, orders.MaxNativeConsumptionEvents+1)
	rows, err := q.Query(ctx, nativeConsumptionSQL, args...)
	if err != nil {
		return facts, ordersError("read scoped native demand consumption", err)
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		var row nativeConsumptionRow
		if err = row.scan(rows); err != nil {
			return facts, ordersError("scan native demand consumption", err)
		}
		bytes += row.bytes()
		if len(facts.Events) >= orders.MaxNativeConsumptionEvents || bytes > orders.MaxNativeConsumptionBytes || !row.valid() {
			facts.Complete = false
			break
		}
		facts.Events = append(facts.Events, row.event)
	}
	return facts, ordersRowsError("finish bounded native demand consumption", rows.Err())
}

// The approval transaction holds the same review-scope lock used at dispatch.
// Recheck after acquiring it and before reserving another native event.
func validateNativeConsumptionIntent(ctx context.Context, q ordersQuerier, work orders.Work, now time.Time) error {
	facts, err := readNativeConsumption(ctx, q, work.Command.Correlation, work.ID, work.CommandFingerprint, now)
	if err != nil {
		return err
	}
	if ordermoney.NativeConsumptionAllows(work.Command, facts).Code != "" {
		return orders.ErrBlocked
	}
	return nil
}

func nativeConsumptionBinding(ctx context.Context, q ordersQuerier, correlation orders.Correlation) (bool, error) {
	if correlation.Validate() != nil || correlation.CustomerOrderID == "" {
		return false, nil
	}
	var bound bool
	args := append(ordersScopeArgs(correlation.Scope), correlation.PostingNumber, correlation.CustomerOrderID, correlation.SyncID, correlation.Marker)
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_projection_bindings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4 AND customer_order_id=$5 AND sync_id=$6 AND marker=$7)`, args...).Scan(&bound)
	return bound, ordersRowsError("verify native demand parent binding", err)
}
