package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func orderDiscoveryStream(stream string) bool {
	return stream == "ozon-orders-discovery" || stream == "ozon-orders-unfulfilled"
}

// This derived lookup index is updated with the same leased source-page commit.
// Immutable page_failures remain the audit; this has no queue/lease of its own.
func updateOrderReadRepairs(ctx context.Context, tx pgx.Tx, page orders.SourcePage, now time.Time) error {
	if !page.FreshDetail || page.Stream == orders.NativePointStream {
		return nil
	}
	var admitted *time.Time
	if err := tx.QueryRow(ctx, `SELECT attempt_started_at FROM orders_ingestion_leases WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND token=$4`, append(ordersScopeArgs(page.Scope), page.FenceToken)...).Scan(&admitted); err != nil {
		return ordersError("read repair admission clock", err)
	}
	if admitted == nil {
		return orders.ErrStale
	}
	if orderDiscoveryStream(page.Stream) && len(page.Failures) > 0 {
		if err := recordOrderDiscoveryFailures(ctx, tx, page, *admitted, now); err != nil {
			return err
		}
	}
	return resolveOrderReadRepairs(ctx, tx, page, *admitted, now)
}

func recordOrderDiscoveryFailures(ctx context.Context, tx pgx.Tx, page orders.SourcePage, admitted, now time.Time) error {
	raw, err := ordersJSON(page.Failures)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_read_repairs(tenant_id,moysklad_account,ozon_account,posting_number,failure_stream,failure_request_key,failure_code,admitted_at,failed_at)
 SELECT $1,$2,$3,f.posting_number,$4,$5,f.code,$7,$8 FROM jsonb_to_recordset($6::jsonb) f(posting_number text,code text)
 ON CONFLICT(tenant_id,moysklad_account,ozon_account,posting_number) DO UPDATE SET
 failure_stream=EXCLUDED.failure_stream,failure_request_key=EXCLUDED.failure_request_key,failure_code=EXCLUDED.failure_code,
 admitted_at=EXCLUDED.admitted_at,failed_at=EXCLUDED.failed_at,resolved_at=NULL,resolution_stream=NULL,resolution_request_key=NULL
 WHERE orders_read_repairs.admitted_at<=EXCLUDED.admitted_at`, append(ordersScopeArgs(page.Scope), page.Stream, page.IdempotencyKey, raw, admitted, now)...)
	return ordersRowsError("index explicit discovery read failures", err)
}

type repairedPostingKey struct {
	Number   string `json:"number"`
	Revision string `json:"revision"`
}

func resolveOrderReadRepairs(ctx context.Context, tx pgx.Tx, page orders.SourcePage, admitted, now time.Time) error {
	if len(page.Postings) == 0 {
		return nil
	}
	keys := make([]repairedPostingKey, 0, len(page.Postings))
	for _, posting := range page.Postings {
		keys = append(keys, repairedPostingKey{posting.PostingNumber, posting.Evidence.Revision})
	}
	raw, err := ordersJSON(keys)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_read_repairs r SET resolved_at=$8,resolution_stream=$4,resolution_request_key=$5
 FROM jsonb_to_recordset($6::jsonb) success(number text,revision text),orders_posting_heads h,orders_posting_observations o
 WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 AND r.resolved_at IS NULL
 AND r.posting_number=success.number AND r.failed_at<=$7
 AND h.tenant_id=r.tenant_id AND h.moysklad_account=r.moysklad_account AND h.ozon_account=r.ozon_account
 AND h.posting_number=r.posting_number AND NOT h.quarantined AND h.checked_at>=$7
 AND o.id=h.observation_id AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account
 AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number AND o.source_revision=success.revision`, append(ordersScopeArgs(page.Scope), page.Stream, page.IdempotencyKey, raw, admitted, now)...)
	return ordersRowsError("resolve actual admitted posting detail failures", err)
}

func applyDiscoveryRepairSummary(ctx context.Context, q ordersQuerier, checkpoint *orders.Checkpoint, page orders.SourcePage) error {
	if !orderDiscoveryStream(page.Stream) {
		return nil
	}
	checkpoint.PageFailures = append([]orders.ReadFailure(nil), page.Failures...)
	failures, more, err := readUnresolvedOrderFailures(ctx, q, page.Scope)
	if err != nil {
		return err
	}
	checkpoint.Failures, checkpoint.FailuresTruncated = failures, more
	checkpoint.Complete = checkpoint.Complete && len(failures) == 0 && !more
	return nil
}

func readUnresolvedOrderFailures(ctx context.Context, q ordersQuerier, scope orders.Scope) ([]orders.ReadFailure, bool, error) {
	rows, err := q.Query(ctx, `SELECT posting_number,failure_code FROM orders_read_repairs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND resolved_at IS NULL ORDER BY posting_number LIMIT 101`, ordersScopeArgs(scope)...)
	if err != nil {
		return nil, false, ordersError("read unresolved order detail summary", err)
	}
	defer rows.Close()
	result := make([]orders.ReadFailure, 0, orders.MaxPagePostings)
	for rows.Next() {
		var failure orders.ReadFailure
		if err = rows.Scan(&failure.PostingNumber, &failure.Code); err != nil {
			return nil, false, ordersError("decode order detail failure", err)
		}
		if len(result) == orders.MaxPagePostings {
			return result, true, nil
		}
		result = append(result, failure)
	}
	return result, false, ordersRowsError("finish order detail failures", rows.Err())
}

func prepareOrderReadCheckpoint(ctx context.Context, tx pgx.Tx, page orders.SourcePage, previous orders.Checkpoint, now time.Time) (orders.Checkpoint, error) {
	if err := updateOrderReadRepairs(ctx, tx, page, now); err != nil {
		return previous, err
	}
	checkpoint := orders.Checkpoint{Scope: page.Scope, Stream: page.Stream, RefreshKey: page.RefreshKey, ConfigRevision: page.ConfigRevision, Credentials: page.Credentials, Revision: previous.Revision + 1, Cursor: page.NextCursor, WindowFrom: page.WindowFrom, WindowTo: page.WindowTo, Complete: page.Complete, Exhausted: page.Exhausted, Failures: page.Failures, FailuresTruncated: page.FailuresTruncated, UpdatedAt: now}
	mergeOrderCheckpointFailures(&checkpoint, previous, page)
	if err := applyDiscoveryRepairSummary(ctx, tx, &checkpoint, page); err != nil {
		return previous, err
	}
	return checkpoint, nil
}
