package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

type nativeDocumentInventoryKey struct {
	WorkID        string `json:"work_id"`
	OzonAccount   string `json:"ozon_account"`
	PostingNumber string `json:"posting_number"`
	NativeOrderID string `json:"native_order_id"`
}

func enrichInventoryNativeDocuments(ctx context.Context, tx pgx.Tx, input *orderinventory.Input, baseline time.Time) error {
	keys := make([]nativeDocumentInventoryKey, 0)
	bytes := 0
	for index := range input.Observations {
		observation := &input.Observations[index]
		if observation.Binding.CustomerOrderID == "" {
			continue
		}
		facts, err := readNativeConsumption(ctx, tx, observation.Binding, "", "", input.Now)
		if err != nil {
			return err
		}
		if facts.Complete && !hasUnpostedNativeConsumption(facts) {
			continue
		}
		raw, err := json.Marshal(facts)
		if err != nil {
			return ordersError("measure native inventory consumption", err)
		}
		bytes += len(raw)
		if bytes > orders.MaxNativeConsumptionBytes {
			input.Truncated = true
			break
		}
		observation.NativeConsumption = &facts
		for _, event := range facts.Events {
			keys = append(keys, nativeDocumentInventoryKey{WorkID: event.Work.ID, OzonAccount: facts.Scope.OzonAccount, PostingNumber: facts.Correlation.PostingNumber, NativeOrderID: facts.Correlation.CustomerOrderID})
		}
	}
	return attachInventoryNativeDocumentPoints(ctx, tx, input, baseline, keys)
}

func hasUnpostedNativeConsumption(facts orders.NativeConsumptionFacts) bool {
	for _, event := range facts.Events {
		if event.Work.Command.NativeMovement != nil && event.Work.Command.NativeMovement.ParentEffect == orders.NativeParentUnpostedDocument {
			return true
		}
	}
	return false
}

func attachInventoryNativeDocumentPoints(ctx context.Context, tx pgx.Tx, input *orderinventory.Input, baseline time.Time, keys []nativeDocumentInventoryKey) error {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) > 1000 {
		input.Truncated = true
		return nil
	}
	points, err := readInventoryNativeDocumentPoints(ctx, tx, input.Pool.Tenant, input.Pool.MoyskladAccount, keys, baseline)
	if err != nil {
		return err
	}
	for index := range input.Observations {
		observation := &input.Observations[index]
		if observation.NativeConsumption == nil {
			continue
		}
		observation.NativeDocumentPoints = make(map[string]orders.NativeDocumentPointEvidence)
		for _, event := range observation.NativeConsumption.Events {
			if point, found := points[event.Work.ID]; found {
				observation.NativeDocumentPoints[event.Work.ID] = point
			}
		}
	}
	return nil
}

func readInventoryNativeDocumentPoints(ctx context.Context, tx pgx.Tx, tenant, account string, keys []nativeDocumentInventoryKey, baseline time.Time) (map[string]orders.NativeDocumentPointEvidence, error) {
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, ordersError("encode native document point keys", err)
	}
	var cutoff *time.Time
	if !baseline.IsZero() {
		cutoff = &baseline
	}
	rows, err := tx.Query(ctx, inventoryNativeDocumentPointsSQL, tenant, account, raw, cutoff)
	if err != nil {
		return nil, ordersError("read inventory native document points", err)
	}
	defer rows.Close()
	result := make(map[string]orders.NativeDocumentPointEvidence, len(keys))
	for rows.Next() {
		point, e := decodeInventoryNativeDocumentPoint(rows, tenant, account)
		if e != nil {
			return nil, e
		}
		result[point.WorkID] = point
	}
	return result, ordersRowsError("finish native document inventory points", rows.Err())
}

const inventoryNativeDocumentPointsSQL = `WITH requested AS (
 SELECT * FROM jsonb_to_recordset($3::jsonb) AS q(work_id text,ozon_account text,posting_number text,native_order_id text)
), latest AS (
 SELECT DISTINCT ON(q.work_id) r.* FROM requested q
 JOIN orders_native_document_reads r ON r.tenant_id=$1 AND r.moysklad_account=$2 AND r.work_id=q.work_id
 AND r.ozon_account=q.ozon_account AND r.posting_number=q.posting_number AND r.customer_order_id=q.native_order_id
 ORDER BY q.work_id,r.admitted_at DESC,r.id DESC
)
SELECT r.work_id,r.ozon_account,r.posting_number,r.customer_order_id,b.sync_id,b.marker,
 s.observation,COALESCE(s.facts_hash,''),r.code,r.credential_revision,COALESCE(c.revision,0),COALESCE(c.enabled,false),
 COALESCE(b.customer_order_id=r.customer_order_id,false),chosen.id IS NOT NULL,
 COALESCE(chosen.started_at,r.started_at),COALESCE(chosen.finished_at,r.finished_at),
 COALESCE(chosen.admitted_at,r.admitted_at),COALESCE(chosen.recorded_at,r.recorded_at),r.recorded_at
 FROM latest r
 LEFT JOIN orders_native_document_snapshots s ON s.id=r.snapshot_id AND s.work_id=r.work_id
 AND s.tenant_id=r.tenant_id AND s.moysklad_account=r.moysklad_account AND s.ozon_account=r.ozon_account
 LEFT JOIN orders_projection_bindings b ON b.tenant_id=r.tenant_id AND b.moysklad_account=r.moysklad_account
 AND b.ozon_account=r.ozon_account AND b.posting_number=r.posting_number
 LEFT JOIN connection_credentials c ON c.tenant_id=r.tenant_id AND c.provider='moysklad' AND c.account_id=r.moysklad_account
 LEFT JOIN LATERAL(SELECT p.id,p.started_at,p.finished_at,p.admitted_at,p.recorded_at
 FROM orders_native_document_reads p WHERE p.work_id=r.work_id AND p.tenant_id=r.tenant_id
 AND p.moysklad_account=r.moysklad_account AND p.ozon_account=r.ozon_account AND p.snapshot_id=r.snapshot_id
 AND p.credential_revision=r.credential_revision AND p.code=''
 AND ($4::timestamptz IS NULL OR p.recorded_at<=$4::timestamptz)
 ORDER BY p.admitted_at DESC,p.id DESC LIMIT 1) chosen ON true`

func decodeInventoryNativeDocumentPoint(row pgx.Row, tenant, account string) (orders.NativeDocumentPointEvidence, error) {
	point := orders.NativeDocumentPointEvidence{Parent: orders.Correlation{Scope: orders.Scope{Tenant: tenant, MoyskladAccount: account}}}
	var raw []byte
	var hash string
	var currentRevision int64
	var enabled, bound bool
	var syncID, marker *string
	err := row.Scan(&point.WorkID, &point.Parent.Scope.OzonAccount, &point.Parent.PostingNumber, &point.Parent.CustomerOrderID, &syncID, &marker, &raw, &hash, &point.Code, &point.CredentialRevision, &currentRevision, &enabled, &bound, &point.Eligible, &point.StartedAt, &point.FinishedAt, &point.AdmittedAt, &point.RecordedAt, &point.CheckedAt)
	if err != nil {
		return point, ordersError("decode native document inventory point", err)
	}
	if syncID != nil && marker != nil {
		point.Parent.SyncID, point.Parent.Marker = *syncID, *marker
	}
	if !enabled || !bound || point.CredentialRevision != currentRevision {
		point.Code, point.Eligible = "NATIVE_BINDING_CHANGED", false
		return point, nil
	}
	if point.Code != "" || len(raw) == 0 {
		return point, nil
	}
	var observation orders.NativeDocumentObservation
	if ordersDecode(raw, &observation) != nil || observation.Validate() != nil || observation.Scope != point.Parent.Scope {
		point.Code, point.Eligible = "NATIVE_ORDER_INCOMPLETE", false
		return point, nil
	}
	actualHash, e := nativeDocumentPointHash(observation)
	if e != nil || actualHash != hash {
		point.Code, point.Eligible = "NATIVE_ORDER_INCOMPLETE", false
		return point, nil
	}
	point.Observation = &observation
	return point, nil
}
