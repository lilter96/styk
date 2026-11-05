package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

type nativeSplitNode struct {
	parent                     string
	complete, consumed, native bool
}

// This is a denial-only ancestry guard, never an allocation or reserve grant.
// An identical commercial order ID alone does not connect independent postings.
func enrichNativeSplitConsumption(ctx context.Context, q ordersQuerier, current *orders.ReviewContext, now time.Time) error {
	current.NativeSplitBlocked = make(map[string]bool, len(current.Postings))
	current.NativeAncestorConsumed = make(map[string]bool, len(current.Postings))
	cache := make(map[string]nativeSplitNode)
	for _, posting := range current.Postings {
		blocked, ancestor, err := nativeSplitConsumptionPath(ctx, q, current, posting.PostingNumber, now, cache)
		if err != nil {
			return err
		}
		current.NativeSplitBlocked[posting.PostingNumber] = blocked
		current.NativeAncestorConsumed[posting.PostingNumber] = ancestor
	}
	return nil
}

func nativeSplitConsumptionPath(ctx context.Context, q ordersQuerier, current *orders.ReviewContext, number string, now time.Time, cache map[string]nativeSplitNode) (bool, bool, error) {
	seen, blocked, ancestor := make(map[string]bool), false, false
	for depth := 0; depth <= orders.MaxSplitLineageDepth; depth++ {
		if seen[number] {
			return true, true, nil
		}
		seen[number] = true
		node, found := cache[number]
		if !found {
			var err error
			node, err = readNativeSplitConsumptionNode(ctx, q, current, number, now)
			if err != nil {
				return false, false, err
			}
			cache[number] = node
		}
		if !node.complete {
			// An unobserved provider ancestor alone is not a native movement.
			// Keep native admission fenced, but do not add a new prohibition
			// on a provider split with no known physical consumption.
			return blocked || node.native, true, nil
		}
		blocked = blocked || node.consumed
		ancestor = ancestor || depth > 0 && node.consumed
		if node.parent == "" {
			return blocked, ancestor, nil
		}
		number = node.parent
	}
	return true, true, nil
}

func readNativeSplitConsumptionNode(ctx context.Context, q ordersQuerier, current *orders.ReviewContext, number string, now time.Time) (nativeSplitNode, error) {
	node, err := readNativeSplitParents(ctx, q, current.Config.Scope, number)
	if err != nil || !node.complete {
		return node, err
	}
	binding, err := readNativePointBinding(ctx, q, current.Config.Scope, number)
	if errors.Is(err, orders.ErrNotFound) || err == nil && binding.CustomerOrderID == "" {
		node.complete = !node.native
		return node, nil
	}
	if err != nil {
		return node, err
	}
	facts, err := readNativeConsumption(ctx, q, binding, current.RevalidationWorkID, current.RevalidationCommandFingerprint, now)
	if err != nil {
		return node, err
	}
	node.consumed = nativeSplitHistoryConsumed(facts)
	return node, nil
}

func nativeSplitHistoryConsumed(facts orders.NativeConsumptionFacts) bool {
	if !facts.Complete {
		return true
	}
	if len(facts.Events) == 0 {
		return false
	}
	seal, issue := ordermoney.NativeConsumptionSnapshot(facts.Events[0].Work.Command, facts)
	if issue.Code != "" {
		return true
	}
	for _, position := range seal.Positions {
		if !orders.ExactDecimalEqual(position.GrossDemand, "0") {
			return true
		}
	}
	return false
}

func readNativeSplitParents(ctx context.Context, q ordersQuerier, scope orders.Scope, number string) (nativeSplitNode, error) {
	rows, err := q.Query(ctx, nativeSplitParentsSQL, append(ordersScopeArgs(scope), number, orders.MaxSplitLineageHeads+1)...)
	if err != nil {
		return nativeSplitNode{}, ordersError("read native consumption ancestry", err)
	}
	defer rows.Close()
	node, count, head := nativeSplitNode{complete: true}, 0, false
	for rows.Next() {
		var kind, parent string
		var raw []byte
		if err = rows.Scan(&kind, &parent, &raw); err != nil {
			return node, ordersError("decode native consumption ancestry", err)
		}
		count++
		if count > orders.MaxSplitLineageHeads || !node.readParent(scope, number, kind, parent, raw, &head) {
			node.complete = false
			break
		}
	}
	node.complete = node.complete && head
	return node, ordersRowsError("finish bounded native consumption ancestry", rows.Err())
}

func (node *nativeSplitNode) readParent(scope orders.Scope, number, kind, parent string, raw []byte, head *bool) bool {
	if kind == "head" {
		var flags struct {
			Quarantined bool
			Native      bool
		}
		if *head || ordersDecode(raw, &flags) != nil || flags.Quarantined {
			return false
		}
		*head, node.native = true, flags.Native
		return true
	}
	switch kind {
	case "provider":
		var valid bool
		parent, valid = nativeProviderParent(raw)
		if !valid || parent == "" {
			return valid
		}
	case "split":
		var children []string
		if ordersDecode(raw, &children) != nil || !nativeSplitChildrenValid(scope, children) {
			return false
		}
	default:
		return false
	}
	if parent == number || node.parent != "" && node.parent != parent {
		return false
	}
	if _, err := orders.CorrelationFor(scope, parent); err != nil {
		return false
	}
	node.parent = parent
	return true
}

func nativeProviderParent(raw []byte) (string, bool) {
	var field orders.Field[string]
	if ordersDecode(raw, &field) != nil || field.Validate() != nil {
		return "", false
	}
	if field.IsMissing() || field.Presence == orders.PresenceNull {
		return "", true
	}
	if !field.IsKnown() {
		return "", false
	}
	return *field.Value, true
}

func nativeSplitChildrenValid(scope orders.Scope, children []string) bool {
	if len(children) < 2 || len(children) > orders.MaxSplitLineageHeads {
		return false
	}
	seen := make(map[string]bool, len(children))
	for _, child := range children {
		if _, err := orders.CorrelationFor(scope, child); err != nil || seen[child] {
			return false
		}
		seen[child] = true
	}
	return true
}

const nativeSplitParentsSQL = `SELECT 'head',h.posting_number,jsonb_build_object('Quarantined',h.quarantined,'Native',
 EXISTS(SELECT 1 FROM orders_work w WHERE w.tenant_id=h.tenant_id AND w.moysklad_account=h.moysklad_account
 AND w.ozon_account=h.ozon_account AND w.posting_number=h.posting_number AND w.action IN('demand_create','salesreturn_create')))
 FROM orders_posting_heads h WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number=$4
 UNION ALL
 SELECT DISTINCT 'provider','',o.posting->'parent_posting_number' FROM orders_posting_observations o
 WHERE o.tenant_id=$1 AND o.moysklad_account=$2 AND o.ozon_account=$3 AND o.posting_number=$4
 AND o.posting->'parent_posting_number' IS NOT NULL
 UNION ALL
 SELECT 'split',w.posting_number,w.work#>'{receipt,posting_numbers}' FROM orders_work w
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND w.action IN('posting_split','posting_split_unassembled') AND w.dispatch_count=1 AND w.posting_number<>$4
 AND w.work#>'{receipt,posting_numbers}' ? $4
 LIMIT $5`
