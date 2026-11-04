package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func validateSupersededSplitDispatch(ctx context.Context, tx pgx.Tx, work orders.Work, guard orders.DispatchGuard, current orders.ReviewContext, now time.Time) error {
	if work.Command.SupersededSplitWorkID == "" || !guard.Begin {
		return nil
	}
	facts, found := current.SplitSupersededReserves[work.Command.PostingNumber]
	if !found || facts.Split.ID != work.Command.SupersededSplitWorkID || guard.SplitParentSource == nil || guard.SplitSupersededTarget == nil {
		return orders.ErrBlocked
	}
	var busy bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_target_fences f JOIN orders_work child ON child.id=f.work_id
 WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND child.dispatch_count=1
 AND child.posting_number=ANY($4::text[]))`, append(ordersScopeArgs(work.Scope), facts.Split.Receipt.PostingNumbers)...).Scan(&busy)
	if err != nil {
		return ordersError("check superseded split child fences", err)
	}
	if busy {
		return orders.ErrSplitTransferBusy
	}
	if !sameSupersededChildHeads(facts.Children, guard.SplitSupersededChildren) {
		return orders.ErrStale
	}
	facts.Parent, facts.Children, facts.Target = *guard.SplitParentSource, guard.SplitSupersededChildren, *guard.SplitSupersededTarget
	if !freshSplitPosting(facts.Parent, now) || !freshSplitParentCheck(facts.Target.ObservedAt, now) {
		return orders.ErrStale
	}
	for _, child := range facts.Children {
		if !freshSplitPosting(child, now) {
			return orders.ErrStale
		}
	}
	return validateSupersededSplitPlan(work.Command, facts)
}

func sameSupersededChildHeads(current, fresh []orders.Posting) bool {
	if len(current) != len(fresh) {
		return false
	}
	heads := make(map[string]string, len(current))
	for _, child := range current {
		hash, err := supersededChildFingerprint(child)
		if err != nil {
			return false
		}
		heads[child.PostingNumber] = hash
	}
	for _, child := range fresh {
		hash, err := supersededChildFingerprint(child)
		if err != nil || heads[child.PostingNumber] != hash {
			return false
		}
		delete(heads, child.PostingNumber)
	}
	return len(heads) == 0
}

func supersededChildFingerprint(child orders.Posting) (string, error) {
	child.PII, child.Customer, child.RelatedPostings = nil, orders.ProtectedCustomer{}, nil
	return orders.PostingFingerprint(child)
}

func freshSplitPosting(posting orders.Posting, now time.Time) bool {
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	return freshSplitParentCheck(checked, now)
}
