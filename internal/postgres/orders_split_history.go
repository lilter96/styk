package postgres

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Only immutable, confirmed point members hydrate historical work. This never
// reads a current head, grants a reserve, or writes the expanded receipt back.
func hydrateHistoricalSplitWork(ctx context.Context, q ordersQuerier, scope orders.Scope, work *orders.Work) error {
	if work == nil || work.Scope != scope || work.Command.Scope != scope {
		return orders.ErrStale
	}
	if work.Command.Action == orders.ActionPostingSplit {
		return nil
	}
	if work.Command.Action != orders.ActionPostingSplitUnassembled || work.State != orders.StateReconciled || work.DispatchCount != 1 || work.Receipt.State != orders.StateReconciled {
		return orders.ErrBlocked
	}
	if err := verifyHistoricalSplitACK(ctx, q, *work); err != nil {
		return err
	}
	candidates, err := orders.UnassembledSplitCandidates(work.Command, work.Receipt)
	if err != nil {
		return err
	}
	numbers := work.Receipt.PostingNumbers
	if len(numbers) < 2 || len(numbers) > 1000 || !slices.IsSorted(numbers) {
		return orders.ErrBlocked
	}
	for i, number := range numbers {
		if !slices.Contains(candidates, number) || i > 0 && numbers[i-1] == number {
			return orders.ErrBlocked
		}
	}
	work.Receipt.ObservedPostings, err = readHistoricalSplitMembers(ctx, q, scope, *work, numbers)
	return err
}

func readHistoricalSplitMembers(ctx context.Context, q ordersQuerier, scope orders.Scope, work orders.Work, numbers []string) ([]orders.Posting, error) {
	rows, err := q.Query(ctx, `SELECT r.posting_number,r.read_code,r.checked_at,o.posting-ARRAY['customer','pii','financial','delivery','requirements','available_actions','related_postings','delivery_profile']::text[]
 FROM orders_split_readbacks r JOIN orders_posting_observations o
 ON o.id=r.observation_id AND o.tenant_id=r.tenant_id AND o.moysklad_account=r.moysklad_account
 AND o.ozon_account=r.ozon_account AND o.posting_number=r.posting_number
 WHERE r.work_id=$4 AND r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3
 AND r.posting_number=ANY($5::text[]) ORDER BY r.posting_number LIMIT 1001`, append(ordersScopeArgs(scope), work.ID, numbers)...)
	if err != nil {
		return nil, ordersError("read immutable split members", err)
	}
	defer rows.Close()
	observed := make([]orders.Posting, 0, len(numbers))
	for rows.Next() {
		cell, err := decodeSplitReadback(rows, scope)
		if err != nil {
			return nil, err
		}
		if cell.Code != "OBSERVED" || cell.Posting == nil || cell.CheckedAt.Before(work.Receipt.ObservedAt) || cell.Posting.CommercialOrderID != work.Command.CommercialOrderID || len(observed) >= len(numbers) || cell.PostingNumber != numbers[len(observed)] {
			return nil, orders.ErrStale
		}
		observed = append(observed, *cell.Posting)
	}
	if err := rows.Err(); err != nil {
		return nil, ordersRowsError("finish immutable split members", err)
	}
	if len(observed) != len(numbers) {
		return nil, orders.ErrStale
	}
	return observed, nil
}

func verifyHistoricalSplitACK(ctx context.Context, q ordersQuerier, work orders.Work) error {
	var raw, commandRaw []byte
	err := q.QueryRow(ctx, `SELECT ack.receipt,w.work->'command' FROM orders_work w JOIN LATERAL
 (SELECT r.receipt FROM orders_receipts r WHERE r.work_id=w.id AND r.tenant_id=w.tenant_id
  AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
  AND r.receipt->>'state'='ACKNOWLEDGED' AND r.receipt->>'http_status'='200'
  AND r.receipt->'unassembled_split' IS NOT NULL ORDER BY r.created_at,r.id LIMIT 1) ack ON true
 WHERE w.id=$4 AND w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND w.action='posting_split_unassembled' AND w.state='RECONCILED' AND w.dispatch_count=1
 AND w.command_fingerprint=$5 AND w.command_id=$6`, append(ordersScopeArgs(work.Scope), work.ID, work.CommandFingerprint, work.Command.ID)...).Scan(&raw, &commandRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrBlocked
	}
	if err != nil {
		return ordersError("read immutable split acknowledgement", err)
	}
	if err = historicalSplitCommandMatches(commandRaw, work.Command); err != nil {
		return err
	}
	var ack orders.Receipt
	if err = ordersDecode(raw, &ack); err != nil {
		return err
	}
	have, err := ordersHash(ack.UnassembledSplit)
	if err != nil {
		return err
	}
	want, err := ordersHash(work.Receipt.UnassembledSplit)
	if err != nil {
		return err
	}
	if ack.CommandID != work.Command.ID || ack.HTTPStatus != 200 || !ack.ObservedAt.Equal(work.Receipt.ObservedAt) || len(ack.PostingNumbers) != 0 || have != want {
		return orders.ErrStale
	}
	return nil
}

func historicalSplitUnavailable(err error) bool {
	return errors.Is(err, orders.ErrBlocked) || errors.Is(err, orders.ErrStale) || errors.Is(err, orders.ErrNotFound)
}

func hydrateSupersededSplitHistory(ctx context.Context, q ordersQuerier, result *orders.ReviewContext) error {
	for number, facts := range result.SplitSupersededReserves {
		if err := hydrateHistoricalSplitWork(ctx, q, result.Config.Scope, &facts.Split); err != nil {
			if historicalSplitUnavailable(err) {
				delete(result.SplitSupersededReserves, number)
				continue
			}
			return err
		}
		result.SplitSupersededReserves[number] = facts
	}
	return nil
}

func decodeSupersededSplitCandidate(row pgx.Row) (orders.SplitSupersededReserveFacts, error) {
	var facts orders.SplitSupersededReserveFacts
	var split, before, correlation, children []byte
	if err := row.Scan(&split, &before, &correlation, &children); err != nil {
		return facts, ordersError("decode superseded split reserve facts", err)
	}
	for _, pair := range []struct {
		raw []byte
		out any
	}{{split, &facts.Split}, {before, &facts.Before}, {correlation, &facts.OriginalCorrelation}, {children, &facts.Children}} {
		if err := ordersDecode(pair.raw, pair.out); err != nil {
			return facts, err
		}
	}
	return facts, nil
}

func historicalSplitCommandMatches(raw []byte, command orders.Command) error {
	var stored orders.Command
	if err := ordersDecode(raw, &stored); err != nil {
		return err
	}
	have, err := ordersHash(stored)
	if err != nil {
		return err
	}
	want, err := ordersHash(command)
	if err != nil {
		return err
	}
	if have != want {
		return orders.ErrStale
	}
	return nil
}
