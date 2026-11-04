package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

type nativePointInventoryRead struct {
	Point                  *orders.NativeOrderPoint
	Code                   string
	AdmittedAt, RecordedAt time.Time
	CredentialRevision     int64
}

type nativeInventoryPointKey struct {
	Index         int    `json:"index"`
	OzonAccount   string `json:"ozon_account"`
	PostingNumber string `json:"posting_number"`
	NativeOrderID string `json:"native_order_id"`
}

type nativeInventoryPointRow struct {
	index                                           int
	raw                                             []byte
	code, syncID, marker, targetRevision, factsHash string
	credentialRevision, currentCredentialRevision   int64
	bindingMatches, credentialEnabled, eligible     bool
	startedAt, finishedAt, admittedAt, recordedAt   time.Time
	observedAt                                      *time.Time
}

// One bounded set read selects the latest attempt, including failures. Provider
// and application clocks never outrank the persisted DB claim admission clock.
// Latest failure or changed facts fences older proofs. An unchanged snapshot may
// reuse a successful attempt recorded before the source baseline began; repeated
// reads do not fabricate a new facts revision or invalidate that earlier proof.
// A zero baseline selects diagnostic evidence without a baseline cutoff.
const inventoryNativePointsSQL = `WITH requested AS (
 SELECT * FROM jsonb_to_recordset($3::jsonb) AS q(index integer,ozon_account text,posting_number text,native_order_id text)
), latest AS (
 SELECT DISTINCT ON(q.index) q.index,r.* FROM requested q
 JOIN orders_native_point_reads r ON r.tenant_id=$1 AND r.moysklad_account=$2
  AND r.ozon_account=q.ozon_account AND r.posting_number=q.posting_number AND r.native_order_id=q.native_order_id
 ORDER BY q.index,r.admitted_at DESC,r.finished_at DESC,r.id DESC
)
SELECT r.index,s.point,r.code,r.sync_id,r.marker,COALESCE(s.target_revision,''),COALESCE(s.facts_hash,''),r.credential_revision,
 COALESCE(c.revision,0),COALESCE(c.enabled,false),
 COALESCE(b.customer_order_id=r.native_order_id AND b.sync_id=r.sync_id AND b.marker=r.marker,false),
 (chosen.id IS NOT NULL),COALESCE(chosen.started_at,r.started_at),COALESCE(chosen.finished_at,r.finished_at),
 COALESCE(chosen.observed_at,r.observed_at),COALESCE(chosen.admitted_at,r.admitted_at),COALESCE(chosen.recorded_at,r.recorded_at)
FROM latest r
LEFT JOIN LATERAL (
 SELECT proof.id,proof.started_at,proof.finished_at,proof.observed_at,proof.admitted_at,proof.recorded_at
 FROM orders_native_point_reads proof
 WHERE proof.tenant_id=r.tenant_id AND proof.moysklad_account=r.moysklad_account AND proof.ozon_account=r.ozon_account
  AND proof.posting_number=r.posting_number AND proof.native_order_id=r.native_order_id AND proof.snapshot_id=r.snapshot_id
  AND proof.credential_revision=r.credential_revision AND proof.sync_id=r.sync_id AND proof.marker=r.marker AND proof.code=''
  AND ($4::timestamptz IS NULL OR proof.recorded_at<=$4::timestamptz)
 ORDER BY proof.admitted_at DESC,proof.finished_at DESC,proof.id DESC LIMIT 1
) chosen ON true
LEFT JOIN orders_native_point_snapshots s ON s.id=r.snapshot_id AND s.tenant_id=r.tenant_id AND s.moysklad_account=r.moysklad_account
 AND s.ozon_account=r.ozon_account AND s.posting_number=r.posting_number AND s.native_order_id=r.native_order_id
LEFT JOIN orders_projection_bindings b ON b.tenant_id=r.tenant_id AND b.moysklad_account=r.moysklad_account
 AND b.ozon_account=r.ozon_account AND b.posting_number=r.posting_number
LEFT JOIN connection_credentials c ON c.tenant_id=r.tenant_id AND c.provider='moysklad' AND c.account_id=r.moysklad_account
ORDER BY r.index`

func readNativePointsForInventory(ctx context.Context, tx pgx.Tx, pool domain.StockPool, observations []orderinventory.Observation, baselineStart time.Time) (map[int]nativePointInventoryRead, error) {
	if len(observations) > 100 {
		return nil, orders.ErrBlocked
	}
	keys, result := inventoryNativePointKeys(pool, observations)
	if len(keys) == 0 {
		return result, nil
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, fmt.Errorf("encode inventory native point keys: %w", err)
	}
	var baseline *time.Time
	if !baselineStart.IsZero() {
		baseline = &baselineStart
	}
	rows, err := tx.Query(ctx, inventoryNativePointsSQL, pool.Tenant, pool.MoyskladAccount, raw, baseline)
	if err != nil {
		return nil, fmt.Errorf("read inventory native points: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		row, scanErr := scanInventoryNativePoint(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result[row.index] = decodeInventoryNativePoint(row, observations[row.index].Binding)
	}
	return result, ordersRowsError("read inventory native points", rows.Err())
}

func inventoryNativePointKeys(pool domain.StockPool, observations []orderinventory.Observation) ([]nativeInventoryPointKey, map[int]nativePointInventoryRead) {
	result := make(map[int]nativePointInventoryRead, len(observations))
	keys := make([]nativeInventoryPointKey, 0, len(observations))
	for index, observation := range observations {
		binding := observation.Binding
		if binding.CustomerOrderID == "" {
			continue
		}
		if binding.Validate() != nil || binding.Scope.Tenant != pool.Tenant || binding.Scope.MoyskladAccount != pool.MoyskladAccount || binding.Scope != observation.Posting.Scope || binding.PostingNumber != observation.Posting.PostingNumber {
			result[index] = nativePointInventoryRead{Code: "NATIVE_BINDING_CHANGED"}
			continue
		}
		keys = append(keys, nativeInventoryPointKey{Index: index, OzonAccount: binding.Scope.OzonAccount, PostingNumber: binding.PostingNumber, NativeOrderID: binding.CustomerOrderID})
	}
	return keys, result
}

func scanInventoryNativePoint(row pgx.Row) (nativeInventoryPointRow, error) {
	var result nativeInventoryPointRow
	err := row.Scan(&result.index, &result.raw, &result.code, &result.syncID, &result.marker, &result.targetRevision, &result.factsHash, &result.credentialRevision, &result.currentCredentialRevision, &result.credentialEnabled, &result.bindingMatches, &result.eligible, &result.startedAt, &result.finishedAt, &result.observedAt, &result.admittedAt, &result.recordedAt)
	return result, ordersRowsError("decode inventory native point read", err)
}

func decodeInventoryNativePoint(row nativeInventoryPointRow, binding orders.Correlation) nativePointInventoryRead {
	result := nativePointInventoryRead{Code: row.code, AdmittedAt: row.admittedAt, RecordedAt: row.recordedAt, CredentialRevision: row.credentialRevision}
	if !row.bindingMatches || row.syncID != binding.SyncID || row.marker != binding.Marker {
		result.Code = "NATIVE_BINDING_CHANGED"
		return result
	}
	if !row.credentialEnabled || row.credentialRevision != row.currentCredentialRevision {
		result.Code = "NATIVE_CREDENTIAL_REVISION_CHANGED"
		return result
	}
	if row.code != "" {
		return result
	}
	if !row.eligible {
		result.Code = "NATIVE_POINT_AFTER_BASELINE"
		return result
	}
	var point orders.NativeOrderPoint
	decoder := json.NewDecoder(bytes.NewReader(row.raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&point) != nil || point.Validate() != nil || point.Correlation != binding || point.Scope != binding.Scope || point.TargetRevision != row.targetRevision || !nativeInventoryFactsMatch(point, row.factsHash) || row.observedAt == nil {
		result.Code = "NATIVE_ORDER_SCHEMA_DRIFT"
		return result
	}
	point.StartedAt, point.FinishedAt, point.ObservedAt = row.startedAt, row.finishedAt, *row.observedAt
	point.CredentialRevision = row.credentialRevision
	if point.Validate() != nil {
		result.Code = "NATIVE_ORDER_SCHEMA_DRIFT"
		return result
	}
	result.Point = &point
	return result
}

func nativeInventoryFactsMatch(point orders.NativeOrderPoint, want string) bool {
	point.Positions = slices.Clone(point.Positions)
	slices.SortFunc(point.Positions, func(a, b orders.TargetPosition) int { return strings.Compare(a.ID, b.ID) })
	point.StartedAt, point.FinishedAt, point.ObservedAt = time.Time{}, time.Time{}, time.Time{}
	point.CredentialRevision = 0
	hash, err := ordersHash(point)
	return err == nil && hash == want
}
