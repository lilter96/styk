package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/orders"
)

func readDeliveryProfiles(ctx context.Context, q ordersQuerier, scope delivery.Scope, now time.Time) ([]delivery.Profile, error) {
	evidence, err := currentDeliveryEvidence()
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON(warehouse_id,delivery_method_id) profile FROM delivery_capability_observations WHERE tenant_id=$1 AND ozon_account=$2 AND contract_hash=$3 AND contract_checked_at=$4 AND checked_at<=$5 AND valid_until>$5 ORDER BY warehouse_id,delivery_method_id,checked_at DESC,id LIMIT 2001`, scope.Tenant, scope.OzonAccount, evidence.Hash, evidence.CheckedAt, now)
	if err != nil {
		return nil, ordersError("read delivery observations", err)
	}
	defer rows.Close()
	result := make([]delivery.Profile, 0, 100)
	for rows.Next() {
		var raw []byte
		var profile delivery.Profile
		if err = rows.Scan(&raw); err != nil {
			return nil, ordersError("scan delivery observation", err)
		}
		if json.Unmarshal(raw, &profile) != nil || profile.Scope != scope || profile.Validate() != nil || !profile.Fresh(now, evidence) {
			return nil, orders.ErrStale
		}
		result = append(result, profile)
	}
	if len(result) > 2000 {
		return nil, orders.ErrBlocked
	}
	return result, ordersRowsError("iterate delivery observations", rows.Err())
}

// enrichOrderWarehouseMappings retains all confirmed mappings and adds one row
// per explicitly observed delivery method. Warehouse-only facts do not invent a
// method ID for FBS postings whose ID happens to equal the warehouse number.
func enrichOrderWarehouseMappings(ctx context.Context, q ordersQuerier, scope orders.Scope, mappings []orders.WarehouseMappingRef) ([]orders.WarehouseMappingRef, error) {
	profiles, err := readDeliveryProfiles(ctx, q, delivery.OrderScope(scope), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	result := make([]orders.WarehouseMappingRef, 0, len(mappings))
	for _, mapping := range mappings {
		matched := false
		for _, profile := range profiles {
			ref := profile.CapabilityRef()
			if profile.WarehouseID != mapping.OzonWarehouseID || ref == nil {
				continue
			}
			copyMapping := mapping
			copyMapping.Capability = *ref
			result = append(result, copyMapping)
			matched = true
		}
		if !matched {
			result = append(result, mapping)
		}
	}
	return result, nil
}
