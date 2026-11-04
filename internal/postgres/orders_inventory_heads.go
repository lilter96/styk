package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

// All Ozon accounts mapping into this tenant's source pool participate. An
// unknown warehouse conservatively affects every warehouse for the same item.
const orderInventoryHeadsSQL = `
SELECT h.projection,source.posting->'status',h.checked_at,h.quarantined,affected.lines,
       COALESCE(b.customer_order_id,''),COALESCE(b.sync_id,''),COALESCE(b.marker,''),w.work,r.observed_target,r.observed_native_document,n.observed_at,
       EXISTS(SELECT 1 FROM orders_work attempted WHERE attempted.tenant_id=h.tenant_id AND attempted.moysklad_account=h.moysklad_account AND attempted.ozon_account=h.ozon_account AND attempted.posting_number=h.posting_number AND attempted.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update') AND attempted.dispatch_count>0)
FROM orders_posting_heads h
JOIN orders_posting_observations source ON source.id=h.observation_id AND source.tenant_id=h.tenant_id AND source.moysklad_account=h.moysklad_account AND source.ozon_account=h.ozon_account AND source.posting_number=h.posting_number
JOIN LATERAL (
 SELECT jsonb_agg(DISTINCT candidates.identity) AS lines FROM (
 SELECT line->'identity' AS identity FROM jsonb_array_elements(h.projection->'lines') line
 WHERE EXISTS (
  SELECT 1 FROM product_mappings m
  JOIN product_mapping_history mh ON mh.mapping_id=m.id AND mh.version=m.version
   AND mh.tenant_id=m.tenant_id AND mh.moysklad_account=m.moysklad_account AND mh.ozon_account=m.ozon_account
   AND mh.target_kind=m.target_kind AND mh.target_id=m.target_id AND mh.ozon_product_id=m.ozon_product_id AND mh.ozon_offer_id=m.ozon_offer_id
  WHERE m.tenant_id=h.tenant_id AND m.moysklad_account=h.moysklad_account AND m.ozon_account=h.ozon_account
   AND m.target_kind=$3 AND m.target_id=$4
   AND (line#>>'{product_id,value}'=m.ozon_product_id OR line#>>'{identity,offer_id}'=m.ozon_offer_id
    OR EXISTS(SELECT 1 FROM ozon_product_observations p WHERE p.import_id=mh.ozon_import_id AND p.product_id=m.ozon_product_id AND p.sku<>'' AND p.sku=line#>>'{identity,sku}'))
 )
 UNION ALL
 SELECT position->'identity' FROM LATERAL (
  SELECT work FROM orders_work previous WHERE previous.tenant_id=h.tenant_id AND previous.moysklad_account=h.moysklad_account AND previous.ozon_account=h.ozon_account AND previous.posting_number=h.posting_number AND previous.state='RECONCILED' AND previous.action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update') ORDER BY previous.created_at DESC,previous.id DESC LIMIT 1
 ) prior, jsonb_array_elements(prior.work#>'{command,customer_order,positions}') position
 WHERE position#>>'{mapping,moysklad_kind}'=$3 AND position#>>'{mapping,moysklad_id}'=$4 AND prior.work#>>'{command,customer_order,store_id}'=$5
 UNION ALL
 SELECT binding#>'{source,identity}' FROM orders_work pending,jsonb_array_elements(pending.work#>'{command,position_replacement,baseline,bindings}') binding
 WHERE pending.tenant_id=h.tenant_id AND pending.moysklad_account=h.moysklad_account AND pending.ozon_account=h.ozon_account AND pending.posting_number=h.posting_number AND pending.action='mirror_composition_update' AND pending.state NOT IN ('RECONCILED','REJECTED','BLOCKED') AND binding#>>'{source,mapping,moysklad_kind}'=$3 AND binding#>>'{source,mapping,moysklad_id}'=$4 AND pending.work#>>'{command,customer_order,store_id}'=$5
 ) candidates
) affected ON affected.lines IS NOT NULL
LEFT JOIN orders_projection_bindings b ON b.tenant_id=h.tenant_id AND b.moysklad_account=h.moysklad_account AND b.ozon_account=h.ozon_account AND b.posting_number=h.posting_number
LEFT JOIN LATERAL (
 SELECT work,id FROM orders_work
 WHERE tenant_id=h.tenant_id AND moysklad_account=h.moysklad_account AND ozon_account=h.ozon_account AND posting_number=h.posting_number
  AND action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update','demand_create','salesreturn_create')
  AND (action IN ('mirror_create','mirror_update','mirror_position_update','mirror_composition_update') OR (COALESCE(work#>>'{command,native_movement,applicable,value}','true')<>'false'
   AND (NOT EXISTS(SELECT 1 FROM orders_native_document_targets nt WHERE nt.work_id=orders_work.id)
    OR EXISTS(SELECT 1 FROM orders_native_document_targets nt WHERE nt.work_id=orders_work.id AND nt.tenant_id=h.tenant_id AND nt.moysklad_account=h.moysklad_account AND nt.ozon_account=h.ozon_account AND nt.store_id=$5 AND nt.item_kind=$3 AND nt.item_id=$4)))) ORDER BY created_at DESC,id DESC LIMIT 1
) w ON true
LEFT JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=h.tenant_id AND r.moysklad_account=h.moysklad_account AND r.ozon_account=h.ozon_account
LEFT JOIN orders_native_document_results n ON n.work_id=w.id AND n.tenant_id=h.tenant_id AND n.moysklad_account=h.moysklad_account AND n.ozon_account=h.ozon_account
WHERE h.tenant_id=$1 AND h.moysklad_account=$2
 AND (NOT EXISTS(SELECT 1 FROM warehouse_mappings wm WHERE wm.tenant_id=h.tenant_id AND wm.moysklad_account=h.moysklad_account AND wm.ozon_account=h.ozon_account AND wm.status='active' AND wm.ozon_warehouse_id=h.projection#>>'{warehouse_id,value}')
  OR EXISTS(SELECT 1 FROM warehouse_mappings wm WHERE wm.tenant_id=h.tenant_id AND wm.moysklad_account=h.moysklad_account AND wm.ozon_account=h.ozon_account AND wm.status='active' AND wm.ozon_warehouse_id=h.projection#>>'{warehouse_id,value}' AND wm.moysklad_warehouse_id=$5))
ORDER BY h.ozon_account,h.posting_number LIMIT 101`

func readOrderInventoryHeads(ctx context.Context, tx pgx.Tx, pool domain.StockPool) ([]orderinventory.Observation, bool, error) {
	rows, err := tx.Query(ctx, orderInventoryHeadsSQL, pool.Tenant, pool.MoyskladAccount, pool.ItemKind, pool.ItemID, pool.WarehouseID)
	if err != nil {
		return nil, false, fmt.Errorf("read order inventory heads: %w", err)
	}
	defer rows.Close()
	result := make([]orderinventory.Observation, 0, 100)
	for rows.Next() {
		observation, scanErr := scanOrderInventoryHead(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		result = append(result, observation)
	}
	if err = rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read order inventory heads: %w", err)
	}
	if len(result) > 100 {
		return result[:100], true, nil
	}
	if err = enrichInventoryPositionOwnership(ctx, tx, result); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func scanOrderInventoryHead(row pgx.Row) (orderinventory.Observation, error) {
	var result orderinventory.Observation
	var posting, status, lines, work, target, native []byte
	var confirmed *time.Time
	var checked time.Time
	if err := row.Scan(&posting, &status, &checked, &result.Quarantined, &lines, &result.Binding.CustomerOrderID, &result.Binding.SyncID, &result.Binding.Marker, &work, &target, &native, &confirmed, &result.ProjectionAttempted); err != nil {
		return result, fmt.Errorf("decode order inventory head: %w", err)
	}
	if err := ordersDecode(posting, &result.Posting); err != nil {
		return result, err
	}
	result.Posting.Evidence.CheckedAt = checked
	// A retained projected status is useful for display, but an omitted status
	// in the selected source observation cannot authorize the terminal branches.
	result.Posting.Status = orders.Field[string]{}
	if len(status) != 0 {
		if err := ordersDecode(status, &result.Posting.Status); err != nil {
			return result, err
		}
	}
	if err := ordersDecode(lines, &result.Lines); err != nil {
		return result, err
	}
	result.Binding.Scope, result.Binding.PostingNumber = result.Posting.Scope, result.Posting.PostingNumber
	if len(work) != 0 {
		result.Work = &orders.Work{}
		if err := ordersDecode(work, result.Work); err != nil {
			return result, err
		}
	}
	if len(target) != 0 {
		result.Target = &orders.TargetObservation{}
		if err := ordersDecode(target, result.Target); err != nil {
			return result, err
		}
	}
	if len(native) != 0 {
		result.NativeDocument = &orders.NativeDocumentObservation{}
		if err := ordersDecode(native, result.NativeDocument); err != nil {
			return result, err
		}
	}
	if confirmed != nil {
		result.NativeConfirmedAt = *confirmed
	}
	return result, nil
}
