package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type deferredOwnedRow struct {
	ownedAncestorNativeRow
	source orders.Posting
}

// Reuse the exact owned-row decoder and extend its scan with immutable preview
// provenance. A source count other than one cannot establish a frozen frontier.
type deferredOwnedScanner struct {
	row    pgx.Row
	source []byte
	count  *int64
}

func (s *deferredOwnedScanner) Scan(dest ...any) error {
	return s.row.Scan(append(dest, &s.source, &s.count)...)
}

const deferredOwnedRowsSQL = `SELECT w.work,r.observed_target,w.created_at,w.dispatched_at,r.created_at,
 EXISTS(SELECT 1 FROM orders_projection_bindings b WHERE b.tenant_id=w.tenant_id
 AND b.moysklad_account=w.moysklad_account AND b.ozon_account=w.ozon_account
 AND b.posting_number=w.posting_number AND b.customer_order_id=r.observed_target#>>'{correlation,customer_order_id}'),
 source.posting-ARRAY['customer','pii','financial','delivery','analytics','requirements','available_actions','related_postings','delivery_profile','order_number']::text[],source.matches
 FROM orders_work w LEFT JOIN orders_reconciliations r ON r.work_id=w.id
 AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
 LEFT JOIN orders_previews p ON p.id=w.work->>'preview_id' AND p.tenant_id=w.tenant_id
 AND p.moysklad_account=w.moysklad_account AND p.ozon_account=w.ozon_account
 LEFT JOIN LATERAL(SELECT d->'posting' posting,count(*) OVER() matches
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.preview->'decisions')='array' THEN p.preview->'decisions' ELSE '[]'::jsonb END) d
 WHERE d#>>'{posting,posting_number}'=w.posting_number
 AND d#>>'{posting,evidence,revision}'=w.work#>>'{command,expected_source_revision}' LIMIT 1) source ON true
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=$4
 AND w.action IN('mirror_create','mirror_update','mirror_position_update','mirror_composition_update')
 AND NOT COALESCE((w.id=$5 AND w.command_fingerprint=$6 AND w.dispatch_count=0 AND w.state IN('QUEUED','CLAIMED')
 AND w.action='mirror_position_update' AND w.work#>>'{command,split_ancestor_settlement,mode}'='retained_reduction'
 AND w.work#>>'{command,split_ancestor_settlement,split_work_id}'=$7
 AND EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.work_id=w.id AND f.tenant_id=w.tenant_id
 AND f.moysklad_account=w.moysklad_account AND f.ozon_account=w.ozon_account AND f.posting_number=w.posting_number)),false)
 ORDER BY w.created_at,w.id LIMIT $8`

func readDeferredOwnedRows(ctx context.Context, q ordersQuerier, scope orders.Scope, spine []orders.SplitLineageStep, selfID, fingerprint string) ([]deferredOwnedRow, error) {
	maximum := 2 * len(spine)
	rows, err := q.Query(ctx, deferredOwnedRowsSQL, append(ordersScopeArgs(scope), spine[0].Before.PostingNumber, selfID, fingerprint, spine[len(spine)-1].Work.ID, maximum+1)...)
	if err != nil {
		return nil, ordersError("read deferred owned history", err)
	}
	defer rows.Close()
	result := make([]deferredOwnedRow, 0, maximum)
	for rows.Next() {
		if len(result) >= maximum {
			return nil, errDeferredOwnedUnsafe
		}
		row, e := decodeDeferredOwnedRow(rows, scope)
		if e != nil {
			return nil, e
		}
		result = append(result, row)
	}
	return result, ordersRowsError("finish deferred owned history", rows.Err())
}

func decodeDeferredOwnedRow(row pgx.Row, scope orders.Scope) (deferredOwnedRow, error) {
	scanner := &deferredOwnedScanner{row: row}
	native, err := decodeOwnedAncestorNativeRow(scanner, scope)
	if err != nil {
		if historicalSplitUnavailable(err) {
			return deferredOwnedRow{}, errDeferredOwnedUnsafe
		}
		return deferredOwnedRow{}, err
	}
	result := deferredOwnedRow{ownedAncestorNativeRow: native}
	if scanner.count == nil || *scanner.count != 1 || ordersDecode(scanner.source, &result.source) != nil || result.source.Validate() != nil || result.source.Scope != scope || result.source.PostingNumber != native.work.Command.PostingNumber || result.source.CommercialOrderID != native.work.Command.CommercialOrderID || result.source.Evidence.Revision != native.work.Command.ExpectedSourceRevision {
		return result, errDeferredOwnedUnsafe
	}
	return result, nil
}

func assignDeferredOwnedRows(f *orders.SplitAncestorSettlementFacts, rows []deferredOwnedRow) bool {
	steps := make(map[string]bool, len(f.Deferred.Spine))
	for _, step := range f.Deferred.Spine {
		steps[step.Work.ID] = true
	}
	used, reduced := make(map[string]bool, len(rows)), make(map[string]bool, len(rows))
	for i, row := range rows {
		w := row.work
		if w.State != orders.StateReconciled || w.DispatchCount != 1 || used[w.ID] {
			return false
		}
		native := orders.SplitDeferredNative{Work: w, Target: row.target, Source: row.source}
		if i == 0 && w.Command.Action == orders.ActionMirrorCreate {
			f.Deferred.Origin = native
		} else {
			id := retainedNativeSplitID(w)
			if id == "" || !steps[id] || reduced[id] || w.Command.Correlation != f.Deferred.Origin.Target.Correlation {
				return false
			}
			reduced[id] = true
			f.Deferred.Reductions = append(f.Deferred.Reductions, native)
		}
		used[w.ID] = true
		f.NativeAttempts = append(f.NativeAttempts, row.attempt)
	}
	return f.Deferred.Origin.Work.ID != ""
}
