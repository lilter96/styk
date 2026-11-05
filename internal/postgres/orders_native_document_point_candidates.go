package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// Existing native-point ingestion owns these reads, leases and checkpoints.
// A result is a known exact document, never an inferred account-wide absence.
func (s *Store) NativeDocumentCandidates(ctx context.Context, scope orders.Scope, limit int, cursor string) (orders.NativeDocumentPointCandidates, error) {
	if scope.Validate() != nil || limit < 1 || limit > orders.MaxPagePostings || len(cursor) > 512 {
		return orders.NativeDocumentPointCandidates{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, nativeDocumentCandidatesSQL, append(ordersScopeArgs(scope), cursor, limit+1)...)
	if err != nil {
		return orders.NativeDocumentPointCandidates{}, ordersError("read due native document points", err)
	}
	defer rows.Close()
	result := orders.NativeDocumentPointCandidates{Documents: make([]orders.NativeDocumentPointCandidate, 0, limit)}
	for rows.Next() {
		candidate := orders.NativeDocumentPointCandidate{Parent: orders.Correlation{Scope: scope}}
		if err = rows.Scan(&candidate.WorkID, &candidate.Parent.PostingNumber, &candidate.Parent.CustomerOrderID, &candidate.Parent.SyncID, &candidate.Parent.Marker, &candidate.Correlation.Kind, &candidate.Correlation.DocumentID, &candidate.Correlation.SyncID, &candidate.Correlation.Marker); err != nil {
			return result, ordersError("decode native document point candidate", err)
		}
		if len(result.Documents) == limit {
			result.NextCursor = result.Documents[limit-1].WorkID
			break
		}
		result.Documents = append(result.Documents, candidate)
	}
	return result, ordersRowsError("finish due native document points", rows.Err())
}

const nativeDocumentCandidatesSQL = `SELECT w.id,n.posting_number,n.customer_order_id,b.sync_id,b.marker,
 n.kind,n.native_document_id::text,i.sync_id::text,i.marker
 FROM orders_native_document_results n
 JOIN orders_native_document_intents i ON i.work_id=n.work_id AND i.tenant_id=n.tenant_id
 AND i.moysklad_account=n.moysklad_account AND i.ozon_account=n.ozon_account
 JOIN orders_work w ON w.id=n.work_id AND w.tenant_id=n.tenant_id
 AND w.moysklad_account=n.moysklad_account AND w.ozon_account=n.ozon_account
 JOIN orders_projection_bindings b ON b.tenant_id=n.tenant_id AND b.moysklad_account=n.moysklad_account
 AND b.ozon_account=n.ozon_account AND b.posting_number=n.posting_number AND b.customer_order_id=n.customer_order_id
 JOIN orders_configs c ON c.tenant_id=n.tenant_id AND c.moysklad_account=n.moysklad_account AND c.ozon_account=n.ozon_account
 JOIN orders_config_revisions cfg ON cfg.tenant_id=c.tenant_id AND cfg.moysklad_account=c.moysklad_account
 AND cfg.ozon_account=c.ozon_account AND cfg.revision=c.revision
 JOIN connection_credentials credential ON credential.tenant_id=n.tenant_id AND credential.provider='moysklad'
 AND credential.account_id=n.moysklad_account
 LEFT JOIN LATERAL(SELECT r.admitted_at,r.credential_revision FROM orders_native_document_reads r
 WHERE r.tenant_id=n.tenant_id AND r.moysklad_account=n.moysklad_account AND r.ozon_account=n.ozon_account
 AND r.work_id=n.work_id ORDER BY r.admitted_at DESC,r.id DESC LIMIT 1) latest ON true
 WHERE n.tenant_id=$1 AND n.moysklad_account=$2 AND n.ozon_account=$3 AND w.id>$4 AND w.state='RECONCILED'
 AND w.work#>>'{command,native_movement,parent_effect}'='UNPOSTED_DOCUMENT_ZERO_COUNTER'
 AND (latest.admitted_at IS NULL OR latest.credential_revision<>credential.revision
 OR latest.admitted_at<=clock_timestamp()-make_interval(secs=>(cfg.config->>'interval_seconds')::int)
 OR n.observed_at>latest.admitted_at)
 ORDER BY w.id LIMIT $5`
