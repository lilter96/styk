package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func loadOwnedForestSettlement(ctx context.Context, q ordersQuerier, nested orders.NestedSplitSettlementFacts, number, selfID, fingerprint string) (orders.SplitAncestorSettlementFacts, error) {
	f := orders.SplitAncestorSettlementFacts{Lineage: nested.Lineage, Attempts: nested.Attempts}
	f.Lineage.SelectedPosting = number
	forest := orders.SplitForestFacts{Lineage: f.Lineage, Attempts: nested.Attempts}
	numbers, node, split, err := ownedForestReadScope(f.Lineage)
	if err != nil {
		return f, err
	}
	anchored, err := ownedForestHasNativeAnchor(ctx, q, f.Lineage, number, selfID, fingerprint, split)
	if err != nil || !anchored {
		if err == nil {
			err = orders.ErrBlocked
		}
		return f, err
	}
	forest.Native, err = readOwnedForestNative(ctx, q, f.Lineage.Scope, numbers, selfID, fingerprint, number, node, split)
	if err != nil {
		return f, err
	}
	if len(forest.Native) == 0 {
		return f, orders.ErrBlocked
	}
	forest.NativeHistory, err = readOwnedForestNativeHistory(ctx, q, f.Lineage.Scope, numbers, selfID, fingerprint, number, node, split)
	if err != nil {
		return f, err
	}
	if err = q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&forest.CheckedAt); err != nil {
		return f, ordersRowsError("read forest settlement clock", err)
	}
	forest.Complete = true
	if _, err = orders.NewSplitForestSettlement(forest); err != nil {
		return f, errDeferredOwnedUnsafe
	}
	f.Forest, f.CheckedAt, f.HistoryUnambiguous = &forest, forest.CheckedAt, true
	return f, nil
}

func ownedForestReadScope(lineage orders.SplitLineageFacts) ([]string, string, string, error) {
	proof, err := orders.NewSplitLineage(lineage)
	if err != nil {
		return nil, "", "", err
	}
	path, err := ownedForestOwnerPath(lineage, proof)
	if err != nil {
		return nil, "", "", orders.ErrBlocked
	}
	numbers := make([]string, 0, len(lineage.CurrentHeads))
	seen := make(map[string]bool, len(lineage.CurrentHeads))
	for _, head := range lineage.CurrentHeads {
		if head.Scope != lineage.Scope || seen[head.PostingNumber] {
			return nil, "", "", orders.ErrBlocked
		}
		seen[head.PostingNumber] = true
		numbers = append(numbers, head.PostingNumber)
	}
	for _, step := range lineage.Splits {
		if step.Work.ID == path[len(path)-1] {
			return numbers, step.Before.PostingNumber, step.Work.ID, nil
		}
	}
	return nil, "", "", orders.ErrBlocked
}

func readOwnedForestNative(ctx context.Context, q ordersQuerier, scope orders.Scope, numbers []string, selfID, fingerprint, selected, node, split string) ([]orders.SplitForestNative, error) {
	maximum := orders.MaxSplitForestNative
	rows, err := q.Query(ctx, ownedForestNativeSQL, append(ordersScopeArgs(scope), numbers, selfID, fingerprint, selected, node, split, maximum+1)...)
	if err != nil {
		return nil, ordersError("read forest native history", err)
	}
	defer rows.Close()
	native := make([]orders.SplitForestNative, 0, maximum)
	for rows.Next() {
		if len(native) >= maximum {
			return nil, errDeferredOwnedUnsafe
		}
		row, decodeErr := decodeDeferredOwnedRow(rows, scope)
		if decodeErr != nil {
			return nil, decodeErr
		}
		native = append(native, orders.SplitForestNative{Work: row.work, Source: row.source, Target: row.target, Attempt: row.attempt})
	}
	return native, ordersRowsError("finish forest native history", rows.Err())
}
