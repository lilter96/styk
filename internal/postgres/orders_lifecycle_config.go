package postgres

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func validateOrderLifecycleChange(req orders.SaveConfigRequest, previous orders.Config, owner string, now time.Time) error {
	next := req.Config.Lifecycle
	if next != nil && next.Enabled && (req.ReadEnabled != nil && !*req.ReadEnabled || req.ReadEnabled == nil && previous.ReadConfigured && !previous.ReadEnabled) {
		return orders.ErrInvalid
	}
	if next == nil || !next.Enabled || req.LifecycleConsent {
		return nil
	}
	prior := previous.Lifecycle
	if prior == nil || prior.OwnerID != owner || !prior.Active(now, previous.Scope, previous.Revision) {
		return orders.ErrBlocked
	}
	if !reflect.DeepEqual(prior.Mappings, next.Mappings) || prior.AutoDemand != next.AutoDemand || prior.AutoReturn != next.AutoReturn || prior.DemandMoment != next.DemandMoment {
		return orders.ErrBlocked
	}
	return nil
}

func finalizeOrderLifecycle(config *orders.Config, previous orders.Config, owner string, now time.Time) error {
	if config.Lifecycle == nil {
		return nil
	}
	policy := *config.Lifecycle
	policy.ID = ""
	if previous.Lifecycle != nil {
		policy.ID = previous.Lifecycle.ID
	}
	if policy.ID == "" {
		var err error
		policy.ID, err = ordersID()
		if err != nil {
			return err
		}
		id := policy.ID
		policy.ID = fmt.Sprintf("%s-%s-4%s-a%s-%s", id[:8], id[8:12], id[13:16], id[17:20], id[20:])
	}
	policy.Scope, policy.OwnerID = config.Scope, owner
	policy.Revision, policy.ConfigRevision, policy.GrantedAt = config.Revision, config.Revision, now
	config.Lifecycle = &policy
	return nil
}

func orderLifecycleActor(policy orders.LifecyclePolicy) string {
	return fmt.Sprintf("LIFECYCLE:%s:%d", policy.ID, policy.Revision)
}

func ordersLifecycleOwner(ctx context.Context, tx pgx.Tx, config orders.Config, actor string) (string, error) {
	policy := config.Lifecycle
	if policy == nil || actor != orderLifecycleActor(*policy) || !config.Enabled || !config.TestEnabled {
		return "", orders.ErrBlocked
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return "", err
	}
	if !policy.Active(now, config.Scope, config.Revision) {
		return "", orders.ErrBlocked
	}
	if err = ordersOwnerCurrent(ctx, tx, config.Scope.Tenant, policy.OwnerID); err != nil {
		return "", err
	}
	if err = validateOrderReadAuthority(ctx, tx, config); err != nil {
		return "", err
	}
	return policy.OwnerID, nil
}
