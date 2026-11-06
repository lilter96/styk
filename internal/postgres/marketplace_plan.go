package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
)

var (
	ErrPlanNotFound = errors.New("merchant plan not found")
	ErrPlanRevision = errors.New("marketplace plan revision conflict")
)

type MarketplacePlanState struct {
	Code     domain.MarketplacePlan
	Revision int64
	Usage    domain.MarketplaceUsage
}

const marketplacePlanStateSQL = `SELECT w.marketplace_plan,w.marketplace_plan_revision,
 COALESCE((SELECT jsonb_object_agg(u.provider,u.used) FROM marketplace_account_usage u WHERE u.tenant_id=w.id),'{}'::jsonb)
 FROM saas_workspaces w JOIN saas_merchant_accounts a ON a.tenant_id=w.id WHERE w.id=$1`

func readMarketplacePlan(ctx context.Context, q credentialQuerier, tenant string) (MarketplacePlanState, error) {
	var state MarketplacePlanState
	err := q.QueryRow(ctx, marketplacePlanStateSQL, tenant).Scan(&state.Code, &state.Revision, &state.Usage)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrPlanNotFound
	}
	if err != nil {
		return state, fmt.Errorf("read marketplace plan: %w", err)
	}
	return state, nil
}

func (s *Store) MarketplacePlan(ctx context.Context, tenant string) (MarketplacePlanState, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readMarketplacePlan(ctx, s.Pool, tenant)
}

// Quota mutations take this lock before merchant actor, request or account
// locks. The usage read is a separate statement after the lock under READ COMMITTED.
func lockMarketplacePlan(ctx context.Context, tx pgx.Tx, tenant string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('marketplace-plan:'||$1,0))`, tenant)
	if err != nil {
		return fmt.Errorf("lock marketplace plan: %w", err)
	}
	return nil
}

func admitMarketplaceAccount(ctx context.Context, tx pgx.Tx, tenant, provider string) error {
	if !domain.CountsTowardMarketplacePlan(provider) {
		return nil
	}
	// Serialize with the trusted legacy workspace binding, which locks w
	// before assigning a merchant account.
	if _, err := tx.Exec(ctx, `SELECT id FROM saas_workspaces WHERE id=$1 FOR SHARE`, tenant); err != nil {
		return fmt.Errorf("lock marketplace workspace: %w", err)
	}
	state, err := readMarketplacePlan(ctx, tx, tenant)
	if errors.Is(err, ErrPlanNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	limits, ok := state.Code.Limits()
	if !ok {
		return fmt.Errorf("unknown marketplace plan")
	}
	return limits.Admit(state.Usage, provider)
}

func (s *Store) ChangeMarketplacePlan(ctx context.Context, tenant string, code domain.MarketplacePlan, expected int64, actor identity.Session) (MarketplacePlanState, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return MarketplacePlanState{}, fmt.Errorf("begin plan change: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockMarketplacePlan(ctx, tx, tenant); err != nil {
		return MarketplacePlanState{}, err
	}
	if err = requirePlanAdminSession(ctx, tx, actor); err != nil {
		return MarketplacePlanState{}, err
	}
	state, err := readMarketplacePlan(ctx, tx, tenant)
	if err != nil {
		return state, err
	}
	if state.Revision != expected {
		return state, ErrPlanRevision
	}
	if state.Code != code {
		if err = updateMarketplacePlan(ctx, tx, tenant, code, state.Code, actor); err != nil {
			return state, err
		}
		state.Code, state.Revision = code, state.Revision+1
	}
	if err = tx.Commit(ctx); err != nil {
		return MarketplacePlanState{}, fmt.Errorf("commit plan change: %w", err)
	}
	return state, nil
}

func requirePlanAdminSession(ctx context.Context, tx pgx.Tx, actor identity.Session) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT u.id FROM saas_sessions s JOIN saas_users u ON u.id=s.user_id
 WHERE s.audit_id=$1 AND u.id=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
 AND s.user_auth_version=u.auth_version AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
 FOR SHARE OF s,u`, actor.AuditID, actor.User.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("verify platform admin session: %w", err)
	}
	return nil
}

func updateMarketplacePlan(ctx context.Context, tx pgx.Tx, tenant string, code, previous domain.MarketplacePlan, actor identity.Session) error {
	tag, err := tx.Exec(ctx, `UPDATE saas_workspaces SET marketplace_plan=$2,marketplace_plan_revision=marketplace_plan_revision+1
 WHERE id=$1 AND EXISTS(SELECT 1 FROM saas_merchant_accounts WHERE tenant_id=$1)`, tenant, code)
	if err != nil {
		return fmt.Errorf("update marketplace plan: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrPlanNotFound
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_security_events(workspace_id,actor_user_id,session_audit_id,action,target)
 VALUES($1,$2,$3,'MARKETPLACE_PLAN_CHANGED',$4)`, tenant, actor.User.ID, actor.AuditID, string(previous)+" -> "+string(code))
	if err != nil {
		return fmt.Errorf("audit marketplace plan: %w", err)
	}
	return nil
}
