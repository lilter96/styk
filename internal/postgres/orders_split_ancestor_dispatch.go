package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func validateOwnedAncestorCommand(c orders.Command, p orders.Posting, current orders.ReviewContext) error {
	actual, exists := current.SplitAncestorSettlements[c.PostingNumber]
	if c.SplitAncestorSettlement == nil {
		if exists {
			return orders.ErrStale
		}
		return nil
	}
	if !exists || !actual.Equal(*c.SplitAncestorSettlement) || !actual.Allows(p) || !actual.AllowsCommand(c) {
		return orders.ErrStale
	}
	return nil
}

// Scope serialization protects local dispatches, not HTTP already sent. A
// pending/UNKNOWN descendant keeps every ancestor fenced against ANY mutation.
func validateOwnedAncestorDispatch(ctx context.Context, tx pgx.Tx, w orders.Work, g orders.DispatchGuard, now time.Time) error {
	var busy bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_target_fences f JOIN orders_work pending ON pending.id=f.work_id AND pending.tenant_id=f.tenant_id AND pending.moysklad_account=f.moysklad_account AND pending.ozon_account=f.ozon_account CROSS JOIN LATERAL jsonb_array_elements(COALESCE(pending.work#>'{command,split_ancestor_settlement,ancestors}','[]'::jsonb)) a WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND pending.dispatch_count=1 AND pending.id<>$5 AND a#>>'{posting,posting_number}'=$4)`, append(ordersScopeArgs(w.Scope), w.Command.PostingNumber, w.ID)...).Scan(&busy)
	if err != nil {
		return ordersError("check owned ancestor descendant fence", err)
	}
	if busy {
		return orders.ErrSplitTransferBusy
	}
	proof := w.Command.SplitAncestorSettlement
	if proof == nil || !g.Begin {
		return nil
	}
	if len(g.SplitAncestorSources) != len(proof.Ancestors) || len(g.SplitAncestorTargets) != len(proof.Ancestors) {
		return orders.ErrBlocked
	}
	for i, a := range proof.Ancestors {
		if !freshSplitPosting(g.SplitAncestorSources[i], now) || !freshSplitParentCheck(g.SplitAncestorTargets[i].ObservedAt, now) || !a.SourceMatches(g.SplitAncestorSources[i]) || !a.TargetMatches(g.SplitAncestorTargets[i]) {
			return orders.ErrStale
		}
	}
	numbers := proof.AncestorNumbers()
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND f.posting_number=ANY($4::text[]) AND f.work_id<>$5)`, append(ordersScopeArgs(w.Scope), numbers, w.ID)...).Scan(&busy)
	if err != nil {
		return ordersError("check owned ancestor mutation fence", err)
	}
	if busy {
		return orders.ErrSplitTransferBusy
	}
	return nil
}

func enrichOwnedAncestorBusy(ctx context.Context, q ordersQuerier, r *orders.ReviewContext, numbers []string) error {
	if len(numbers) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT a#>>'{posting,posting_number}' FROM orders_target_fences f JOIN orders_work w ON w.id=f.work_id AND w.tenant_id=f.tenant_id AND w.moysklad_account=f.moysklad_account AND w.ozon_account=f.ozon_account CROSS JOIN LATERAL jsonb_array_elements(COALESCE(w.work#>'{command,split_ancestor_settlement,ancestors}','[]'::jsonb)) a WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND w.dispatch_count=1 AND a#>>'{posting,posting_number}'=ANY($4::text[]) LIMIT 129`, append(ordersScopeArgs(r.Config.Scope), numbers)...)
	if err != nil {
		return ordersError("read owned ancestor busy groups", err)
	}
	defer rows.Close()
	if r.SplitTransferBusy == nil {
		r.SplitTransferBusy = make(map[string]bool)
	}
	for rows.Next() {
		var number string
		if err = rows.Scan(&number); err != nil {
			return ordersError("decode owned ancestor busy group", err)
		}
		r.SplitTransferBusy[number] = true
	}
	return ordersRowsError("finish owned ancestor busy groups", rows.Err())
}
