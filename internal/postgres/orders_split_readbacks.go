package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

const splitReadbackRowsSQL = `SELECT r.posting_number,
 CASE WHEN r.read_code='SOURCE_UNAVAILABLE' THEN r.read_code
  WHEN h.observation_id=r.observation_id AND NOT h.quarantined THEN r.read_code ELSE 'SOURCE_CHANGED' END,
 r.checked_at,CASE WHEN r.read_code='OBSERVED' AND h.observation_id=r.observation_id AND NOT h.quarantined
  THEN o.posting-ARRAY['customer','pii','financial','delivery','requirements','available_actions','related_postings','delivery_profile']::text[] ELSE NULL END
 FROM orders_split_readbacks r LEFT JOIN orders_posting_observations o
 ON o.id=r.observation_id AND o.tenant_id=r.tenant_id AND o.moysklad_account=r.moysklad_account
 AND o.ozon_account=r.ozon_account AND o.posting_number=r.posting_number
 LEFT JOIN orders_posting_heads h ON h.tenant_id=r.tenant_id AND h.moysklad_account=r.moysklad_account
 AND h.ozon_account=r.ozon_account AND h.posting_number=r.posting_number
 WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 AND r.work_id=$4
 ORDER BY r.posting_number LIMIT 1002`

// OrderSplitReadbacks loads bounded allocation facts from immutable source
// observations. Sensitive customer and unrelated financial blocks stay in their
// original protected storage and never expand the durable work JSON.
func (s *Store) OrderSplitReadbacks(ctx context.Context, scope orders.Scope, workID string) ([]orders.SplitReadback, error) {
	if scope.Validate() != nil || workID == "" {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderSplitReadbacks(ctx, s.Pool, scope, workID)
}

func readOrderSplitReadbacks(ctx context.Context, q ordersQuerier, scope orders.Scope, workID string) ([]orders.SplitReadback, error) {
	if scope.Validate() != nil || workID == "" {
		return nil, orders.ErrInvalid
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_work WHERE id=$4 AND tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3)`, append(ordersScopeArgs(scope), workID)...).Scan(&exists); err != nil {
		return nil, ordersError("check split readback owner", err)
	}
	if !exists {
		return nil, orders.ErrNotFound
	}
	rows, err := q.Query(ctx, splitReadbackRowsSQL, append(ordersScopeArgs(scope), workID)...)
	if err != nil {
		return nil, ordersError("read normalized split facts", err)
	}
	defer rows.Close()
	result := make([]orders.SplitReadback, 0)
	for rows.Next() {
		cell, err := decodeSplitReadback(rows, scope)
		if err != nil {
			return nil, err
		}
		if len(result) >= 1001 {
			return nil, orders.ErrBlocked
		}
		result = append(result, cell)
	}
	return result, ordersRowsError("finish normalized split facts", rows.Err())
}

func decodeSplitReadback(row pgx.Row, scope orders.Scope) (orders.SplitReadback, error) {
	var result orders.SplitReadback
	var raw []byte
	if err := row.Scan(&result.PostingNumber, &result.Code, &result.CheckedAt, &raw); err != nil {
		return result, ordersError("decode normalized split fact", err)
	}
	if (result.Code == "SOURCE_UNAVAILABLE" || result.Code == "SOURCE_CHANGED") && len(raw) == 0 {
		return result, nil
	}
	if result.Code != "OBSERVED" || len(raw) == 0 {
		return result, orders.ErrStale
	}
	posting := new(orders.Posting)
	if err := ordersDecode(raw, posting); err != nil {
		return result, err
	}
	if posting.Validate() != nil || posting.Scope != scope || posting.PostingNumber != result.PostingNumber {
		return result, orders.ErrStale
	}
	posting.PII, posting.Customer = nil, orders.ProtectedCustomer{}
	posting.Evidence.CheckedAt = result.CheckedAt
	result.Posting = posting
	return result, nil
}

func saveUnassembledSplitReadbacks(ctx context.Context, tx pgx.Tx, work orders.Work, completion orders.Completion, now time.Time) error {
	if len(completion.SplitReads) == 0 {
		return nil
	}
	if len(completion.SplitReads) > 10 || work.Command.Action != orders.ActionPostingSplitUnassembled || !work.State.ReadOnly() || work.DispatchCount != 1 {
		return orders.ErrInvalid
	}
	candidates, err := orders.UnassembledSplitCandidates(work.Command, work.Receipt)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(candidates))
	for _, number := range candidates {
		allowed[number] = true
	}
	seen := make(map[string]bool, len(completion.SplitReads))
	for _, cell := range completion.SplitReads {
		if seen[cell.PostingNumber] || !allowed[cell.PostingNumber] || !validSplitReadback(work, cell, now) {
			return orders.ErrStale
		}
		seen[cell.PostingNumber] = true
	}
	sequence, idle, err := splitReadbackSequence(ctx, tx, work, completion.SplitReads)
	if err != nil {
		return err
	}
	for _, cell := range completion.SplitReads {
		if err := saveSplitReadbackCell(ctx, tx, work, cell, sequence, idle, now); err != nil {
			return err
		}
	}
	return nil
}

func validSplitReadback(work orders.Work, cell orders.SplitReadback, now time.Time) bool {
	if cell.CheckedAt.IsZero() || cell.CheckedAt.Before(work.Receipt.ObservedAt) || cell.CheckedAt.After(now) {
		return false
	}
	if cell.Code == "SOURCE_UNAVAILABLE" {
		return cell.Posting == nil
	}
	if cell.Code != "OBSERVED" || cell.Posting == nil {
		return false
	}
	posting := cell.Posting
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	return posting.Validate() == nil && posting.Scope == work.Scope && posting.CommercialOrderID == work.Command.CommercialOrderID && posting.PostingNumber == cell.PostingNumber && posting.Evidence.Origin == orders.OriginOzon && checked.Equal(cell.CheckedAt) && !posting.Evidence.ObservedAt.After(now)
}

func splitReadbackSequence(ctx context.Context, tx pgx.Tx, work orders.Work, reads []orders.SplitReadback) (int64, bool, error) {
	for _, cell := range reads {
		if cell.Posting != nil {
			return fulfillmentReadbackSequence(ctx, tx, work.Scope)
		}
	}
	return 0, false, nil
}

func saveSplitReadbackCell(ctx context.Context, tx pgx.Tx, work orders.Work, cell orders.SplitReadback, sequence int64, idle bool, now time.Time) error {
	var observationID *string
	if cell.Posting != nil {
		if err := insertFulfillmentReadback(ctx, tx, work, *cell.Posting, sequence, idle, now); err != nil {
			return err
		}
		var id string
		args := append(ordersScopeArgs(work.Scope), cell.PostingNumber, cell.Posting.Evidence.Revision)
		if err := tx.QueryRow(ctx, `SELECT id FROM orders_posting_observations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4 AND source_revision=$5`, args...).Scan(&id); err != nil {
			return ordersError("link immutable split observation", err)
		}
		observationID = &id
	}
	var saved string
	err := tx.QueryRow(ctx, `INSERT INTO orders_split_readbacks(work_id,tenant_id,moysklad_account,ozon_account,posting_number,observation_id,read_code,checked_at,recorded_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(work_id,posting_number) DO UPDATE
 SET observation_id=EXCLUDED.observation_id,read_code=EXCLUDED.read_code,checked_at=EXCLUDED.checked_at,
 recorded_at=CASE WHEN orders_split_readbacks.checked_at<EXCLUDED.checked_at THEN EXCLUDED.recorded_at ELSE orders_split_readbacks.recorded_at END
 WHERE orders_split_readbacks.checked_at<EXCLUDED.checked_at OR (orders_split_readbacks.checked_at=EXCLUDED.checked_at
  AND orders_split_readbacks.read_code=EXCLUDED.read_code AND orders_split_readbacks.observation_id IS NOT DISTINCT FROM EXCLUDED.observation_id)
 RETURNING posting_number`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, cell.PostingNumber, observationID, cell.Code, cell.CheckedAt, now).Scan(&saved)
	if errors.Is(err, pgx.ErrNoRows) {
		return orders.ErrStale
	}
	return ordersRowsError("save normalized split readback", err)
}
