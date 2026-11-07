package postgres

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func validateCommissionConsent(req orders.SaveConfigRequest, prior orders.Config, owner string, now time.Time) error {
	next := req.Config.Finance
	if next == nil || !next.Enabled {
		return nil
	}
	if req.ReadEnabled != nil && !*req.ReadEnabled || req.ReadEnabled == nil && !prior.ReadsEnabled() && !req.Config.ReadsEnabled() {
		return orders.ErrInvalid
	}
	if req.FinanceConsent {
		return nil
	}
	if prior.Finance == nil || prior.Finance.OwnerID != owner || !prior.Finance.Active(now, prior.Scope, prior.Revision) {
		return orders.ErrBlocked
	}
	a, b := *next, *prior.Finance
	if a.Automatic && !b.Automatic {
		return orders.ErrBlocked
	}
	a.ID, a.Scope, a.Revision, a.ConfigRevision, a.OwnerID, a.GrantedAt, a.Automatic = b.ID, b.Scope, b.Revision, b.ConfigRevision, b.OwnerID, b.GrantedAt, b.Automatic
	if !reflect.DeepEqual(a, b) {
		return orders.ErrBlocked
	}
	return nil
}

func finalizeCommissionPolicy(config *orders.Config, previous orders.Config, owner string, now time.Time) error {
	if config.Finance == nil {
		return nil
	}
	policy := *config.Finance
	policy.ID = ""
	if previous.Finance != nil {
		policy.ID = previous.Finance.ID
	}
	if policy.ID == "" {
		var err error
		policy.ID, err = ordersID()
		if err != nil {
			return err
		}
	}
	policy.Scope, policy.OwnerID, policy.GrantedAt = config.Scope, owner, now
	policy.Revision, policy.ConfigRevision = config.Revision, config.Revision
	config.Finance = &policy
	return nil
}

func commissionActor(policy orders.CommissionPolicy) string {
	return fmt.Sprintf("FINANCE:%s:%d", policy.ID, policy.Revision)
}

func commissionOwner(ctx context.Context, tx pgx.Tx, config orders.Config, actor string, automatic bool) (string, error) {
	if !automatic {
		return ordersActor(ctx, tx, config.Scope.Tenant, actor)
	}
	policy := config.Finance
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return "", err
	}
	if policy == nil || !policy.Automatic || actor != commissionActor(*policy) || !policy.Active(now, config.Scope, config.Revision) {
		return "", orders.ErrBlocked
	}
	if err = ordersOwnerCurrent(ctx, tx, config.Scope.Tenant, policy.OwnerID); err != nil {
		return "", err
	}
	return policy.OwnerID, nil
}

func writeCommissionSchedule(ctx context.Context, tx pgx.Tx, config orders.Config) error {
	if config.Finance == nil || !config.Finance.Enabled {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO orders_finance_schedule(tenant_id,moysklad_account,ozon_account) VALUES($1,$2,$3) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET next_run_at=clock_timestamp()`, ordersScopeArgs(config.Scope)...)
	return ordersRowsError("schedule current commission policy", err)
}
