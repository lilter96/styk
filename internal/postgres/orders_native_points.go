package postgres

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// Called only after the existing ingestion page journal is inserted, in the
// same transaction. Failed native reads are evidence, never successful zeros.
func saveOrderNativePointPage(ctx context.Context, tx pgx.Tx, page orders.SourcePage, now time.Time) error {
	if page.Stream != orders.NativePointStream {
		if len(page.NativeReads) > 0 {
			return orders.ErrInvalid
		}
		return nil
	}
	var pageID string
	err := tx.QueryRow(ctx, `SELECT id FROM orders_ingestion_pages WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND stream=$4 AND request_key=$5`, append(ordersScopeArgs(page.Scope), page.Stream, page.IdempotencyKey)...).Scan(&pageID)
	if err != nil {
		return ordersError("read journaled native point page", err)
	}
	var admitted *time.Time
	err = tx.QueryRow(ctx, `SELECT attempt_started_at FROM orders_ingestion_leases WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND stream=$4 AND token=$5`, append(ordersScopeArgs(page.Scope), page.Stream, page.FenceToken)...).Scan(&admitted)
	if err != nil {
		return ordersError("read native point admission clock", err)
	}
	if admitted == nil || admitted.IsZero() || admitted.After(now) {
		return orders.ErrStale
	}
	for _, read := range page.NativeReads {
		if err = saveOrderNativePointRead(ctx, tx, page, read, pageID, *admitted, now); err != nil {
			return err
		}
	}
	for _, read := range page.NativeDocumentReads {
		if err = saveNativeDocumentPointRead(ctx, tx, page, read, pageID, *admitted, now); err != nil {
			return err
		}
	}
	return nil
}

func saveOrderNativePointRead(ctx context.Context, tx pgx.Tx, page orders.SourcePage, read orders.NativeOrderPointRead, pageID string, admittedAt, now time.Time) error {
	if read.Validate() != nil || read.Correlation.Scope != page.Scope || read.CredentialRevision != page.Credentials.Moysklad || read.StartedAt.Before(page.StartedAt) || read.FinishedAt.After(page.FinishedAt) || read.FinishedAt.After(now.Add(5*time.Second)) {
		return orders.ErrStale
	}
	binding, err := readNativePointBinding(ctx, tx, page.Scope, read.Correlation.PostingNumber)
	if err != nil {
		return err
	}
	if binding != read.Correlation {
		return orders.ErrStale
	}
	var snapshotID *int64
	var observedAt *time.Time
	if read.Point != nil {
		id, e := saveOrderNativePointSnapshot(ctx, tx, *read.Point, now)
		if e != nil {
			return e
		}
		snapshotID = &id
		observedAt = &read.Point.ObservedAt
	}
	args := append(ordersScopeArgs(page.Scope), binding.PostingNumber, binding.CustomerOrderID, binding.SyncID, binding.Marker, read.CredentialRevision, pageID, snapshotID, read.Code, read.StartedAt, read.FinishedAt, now, observedAt, admittedAt)
	_, err = tx.Exec(ctx, `INSERT INTO orders_native_point_reads(tenant_id,moysklad_account,ozon_account,posting_number,native_order_id,sync_id,marker,credential_revision,page_id,snapshot_id,code,started_at,finished_at,recorded_at,observed_at,admitted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, args...)
	return ordersRowsError("save actual native point read", err)
}

func saveOrderNativePointSnapshot(ctx context.Context, tx pgx.Tx, point orders.NativeOrderPoint, now time.Time) (int64, error) {
	facts := point
	facts.Positions = slices.Clone(point.Positions)
	slices.SortFunc(facts.Positions, func(a, b orders.TargetPosition) int { return strings.Compare(a.ID, b.ID) })
	facts.StartedAt, facts.FinishedAt, facts.ObservedAt = time.Time{}, time.Time{}, time.Time{}
	facts.CredentialRevision = 0
	hash, err := ordersHash(facts)
	if err != nil {
		return 0, err
	}
	raw, err := ordersJSON(point)
	if err != nil {
		return 0, err
	}
	args := append(ordersScopeArgs(point.Scope), point.Correlation.PostingNumber, point.Correlation.CustomerOrderID, point.TargetRevision, hash, raw, now)
	if _, err = tx.Exec(ctx, `INSERT INTO orders_native_point_snapshots(tenant_id,moysklad_account,ozon_account,posting_number,native_order_id,target_revision,facts_hash,point,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(tenant_id,moysklad_account,ozon_account,native_order_id,target_revision) DO NOTHING`, args...); err != nil {
		return 0, ordersError("save immutable native order facts", err)
	}
	var id int64
	var previous string
	err = tx.QueryRow(ctx, `SELECT id,facts_hash FROM orders_native_point_snapshots WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND native_order_id=$4 AND target_revision=$5`, append(ordersScopeArgs(point.Scope), point.Correlation.CustomerOrderID, point.TargetRevision)...).Scan(&id, &previous)
	if err != nil {
		return 0, ordersError("read deduplicated native order facts", err)
	}
	if previous != hash {
		return 0, orders.ErrConflict
	}
	return id, nil
}

// NativeOrderCandidates is a keyset page of bound native documents needing a
// point read. Terminal orders remain eligible; creation-date windows are not a
// stream of later native edits. This method never returns an account-coverage claim.
func (s *Store) NativeOrderCandidates(ctx context.Context, scope orders.Scope, limit int, cursor string) (orders.NativePointCandidates, error) {
	if scope.Validate() != nil || limit < 1 || limit > orders.MaxPagePostings || len(cursor) > 512 {
		return orders.NativePointCandidates{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, nativeOrderCandidatesSQL, append(ordersScopeArgs(scope), cursor, limit+1)...)
	if err != nil {
		return orders.NativePointCandidates{}, ordersError("read due native order points", err)
	}
	defer rows.Close()
	result := orders.NativePointCandidates{Correlations: make([]orders.Correlation, 0, limit)}
	for rows.Next() {
		correlation := orders.Correlation{Scope: scope}
		if err = rows.Scan(&correlation.PostingNumber, &correlation.CustomerOrderID, &correlation.SyncID, &correlation.Marker); err != nil {
			return result, ordersError("decode native point candidate", err)
		}
		if len(result.Correlations) == limit {
			result.NextCursor = result.Correlations[limit-1].PostingNumber
			break
		}
		result.Correlations = append(result.Correlations, correlation)
	}
	return result, ordersRowsError("read native point candidates", rows.Err())
}

const nativeOrderCandidatesSQL = `SELECT b.posting_number,b.customer_order_id,b.sync_id,b.marker
 FROM orders_projection_bindings b
 JOIN orders_posting_heads h ON h.tenant_id=b.tenant_id AND h.moysklad_account=b.moysklad_account AND h.ozon_account=b.ozon_account AND h.posting_number=b.posting_number
 JOIN orders_configs c ON c.tenant_id=b.tenant_id AND c.moysklad_account=b.moysklad_account AND c.ozon_account=b.ozon_account
 JOIN orders_config_revisions cfg ON cfg.tenant_id=c.tenant_id AND cfg.moysklad_account=c.moysklad_account AND cfg.ozon_account=c.ozon_account AND cfg.revision=c.revision
 JOIN connection_credentials credential ON credential.tenant_id=b.tenant_id AND credential.provider='moysklad' AND credential.account_id=b.moysklad_account
 LEFT JOIN LATERAL(SELECT r.finished_at,r.admitted_at,r.snapshot_id,r.credential_revision,r.native_order_id FROM orders_native_point_reads r WHERE r.tenant_id=b.tenant_id AND r.moysklad_account=b.moysklad_account AND r.ozon_account=b.ozon_account AND r.posting_number=b.posting_number ORDER BY r.admitted_at DESC,r.finished_at DESC,r.id DESC LIMIT 1) latest ON true
 LEFT JOIN orders_native_point_snapshots snapshot ON snapshot.id=latest.snapshot_id
 LEFT JOIN LATERAL(SELECT r.created_at,r.observed_target->>'revision' AS target_revision FROM orders_reconciliations r WHERE r.tenant_id=b.tenant_id AND r.moysklad_account=b.moysklad_account AND r.ozon_account=b.ozon_account AND r.correlation->>'posting_number'=b.posting_number AND r.correlation->>'customer_order_id'=b.customer_order_id AND r.observed_target IS NOT NULL ORDER BY r.created_at DESC,r.work_id DESC LIMIT 1) reconciliation ON true
 WHERE b.tenant_id=$1 AND b.moysklad_account=$2 AND b.ozon_account=$3 AND b.posting_number>$4 AND b.customer_order_id IS NOT NULL
 AND (latest.finished_at IS NULL OR latest.native_order_id<>b.customer_order_id OR latest.credential_revision<>credential.revision OR latest.admitted_at<=clock_timestamp()-make_interval(secs=>(cfg.config->>'interval_seconds')::int)
 OR reconciliation.created_at>latest.admitted_at
 OR (latest.snapshot_id IS NOT NULL AND reconciliation.target_revision<>snapshot.target_revision))
 ORDER BY b.posting_number LIMIT $5`

func readNativePointBinding(ctx context.Context, q ordersQuerier, scope orders.Scope, number string) (orders.Correlation, error) {
	result := orders.Correlation{Scope: scope, PostingNumber: number}
	err := q.QueryRow(ctx, `SELECT sync_id,marker,COALESCE(customer_order_id,'') FROM orders_projection_bindings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4`, append(ordersScopeArgs(scope), number)...).Scan(&result.SyncID, &result.Marker, &result.CustomerOrderID)
	return result, ordersRowsError("read exact native point binding", err)
}
