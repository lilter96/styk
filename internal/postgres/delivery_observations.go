package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

var _ delivery.Observer = (*Store)(nil)

func (s *Store) SaveDeliveryObservations(ctx context.Context, scope delivery.Scope, profiles []delivery.Profile) error {
	if scope.Validate() != nil || len(profiles) > 2000 {
		return orders.ErrInvalid
	}
	evidence, err := currentDeliveryEvidence()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin delivery observations", err)
	}
	defer rollbackTransaction(tx)
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.Scope != scope || profile.Validate() != nil || !profile.Fresh(now, evidence) {
			return orders.ErrStale
		}
		if err = saveDeliveryProfile(ctx, tx, profile); err != nil {
			return err
		}
	}
	return ordersRowsError("commit delivery observations", tx.Commit(ctx))
}

func saveDeliveryProfile(ctx context.Context, tx pgx.Tx, profile delivery.Profile) error {
	raw, err := json.Marshal(profile)
	if err != nil {
		return orders.ErrInvalid
	}
	method := ""
	if profile.MethodID.IsKnown() {
		method = *profile.MethodID.Value
	}
	tag, err := tx.Exec(ctx, `INSERT INTO delivery_capability_observations(tenant_id,ozon_account,warehouse_id,delivery_method_id,revision,id,contract_hash,contract_checked_at,profile,observed_at,checked_at,valid_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(tenant_id,ozon_account,warehouse_id,delivery_method_id,revision) DO UPDATE SET checked_at=EXCLUDED.checked_at,valid_until=EXCLUDED.valid_until,profile=EXCLUDED.profile||jsonb_build_object('observed_at',delivery_capability_observations.observed_at) WHERE delivery_capability_observations.id=EXCLUDED.id AND delivery_capability_observations.contract_hash=EXCLUDED.contract_hash AND delivery_capability_observations.checked_at<=EXCLUDED.checked_at`, profile.Scope.Tenant, profile.Scope.OzonAccount, profile.WarehouseID, method, profile.Revision, profile.ID, profile.Contract.Hash, profile.Contract.CheckedAt, raw, profile.ObservedAt, profile.CheckedAt, profile.ValidUntil)
	if err != nil {
		return ordersError("save delivery observation", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrStale
	}
	return nil
}

func currentDeliveryEvidence() (delivery.ContractEvidence, error) {
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		return delivery.ContractEvidence{}, err
	}
	return delivery.Evidence(manifest)
}

func (s *Store) DeliveryObservations(ctx context.Context, scope delivery.Scope) ([]delivery.Profile, error) {
	if scope.Validate() != nil {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readDeliveryProfiles(ctx, s.Pool, scope, time.Now().UTC())
}
