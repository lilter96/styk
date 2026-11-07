package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) SaveCommissionPreview(ctx context.Context, preview orders.CommissionPreview) (orders.CommissionPreview, error) {
	if preview.ID == "" {
		preview.ID = preview.Plan.ID
	}
	if preview.ID != preview.Plan.ID || preview.Plan.Scope.Validate() != nil {
		return preview, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return preview, ordersError("begin commission preview", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, preview.Plan.Scope); err != nil {
		return preview, err
	}
	config, err := readOrderConfig(ctx, tx, preview.Plan.Scope)
	if err != nil {
		return preview, err
	}
	owner, err := commissionOwner(ctx, tx, config, preview.Actor, preview.Automatic)
	if err != nil {
		return preview, err
	}
	if err = validateCommissionPreview(ctx, tx, preview, config, owner); err != nil {
		return preview, err
	}
	if err = observeCommissionSource(ctx, tx, preview.Plan.Source); err != nil {
		return preview, err
	}
	preview, err = commissionPreviewDiff(ctx, tx, preview)
	if err != nil {
		return preview, err
	}
	if err = insertCommissionPreview(ctx, tx, preview, owner); err != nil {
		return preview, err
	}
	return preview, ordersRowsError("commit commission preview", tx.Commit(ctx))
}

func commissionPreviewDiff(ctx context.Context, tx pgx.Tx, preview orders.CommissionPreview) (orders.CommissionPreview, error) {
	var err error
	preview.ExistingWork, err = readCommissionPeriod(ctx, tx, preview.Plan.Scope, preview.Plan.Source.Period)
	if err != nil {
		return preview, err
	}
	preview.SourceChanged = preview.ExistingWork != nil && preview.ExistingWork.Plan.Source.Fingerprint != preview.Plan.Source.Fingerprint
	if preview.SourceChanged {
		preview.SourceDiff, err = ordermoney.FinancialSourceDiff(preview.ExistingWork.Plan.Source, preview.Plan.Source)
		if err != nil {
			return preview, err
		}
	}
	return preview, nil
}

func validateCommissionPreview(ctx context.Context, q pgx.Tx, preview orders.CommissionPreview, config orders.Config, owner string) error {
	plan := preview.Plan
	now, err := ordersClock(ctx, q)
	if err != nil {
		return err
	}
	if !config.ReadsEnabled() {
		return orders.ErrBlocked
	}
	if err = validateOrderReadAuthority(ctx, q, config); err != nil {
		return err
	}
	if config.Revision != plan.ConfigRevision || plan.Source.Scope != config.Scope || plan.Source.Period.Validate(now) != nil || len(plan.Source.Fingerprint) != 64 || plan.Source.ObservedAt.IsZero() || plan.Source.ObservedAt.After(now.Add(time.Minute)) || !plan.ExpiresAt.After(now) || plan.CreatedAt.After(now.Add(time.Minute)) || plan.ExpiresAt.Sub(plan.CreatedAt) > 15*time.Minute {
		return orders.ErrStale
	}
	credentials, err := ordersCredentials(ctx, q, config.Scope)
	if err != nil {
		return err
	}
	if credentials != plan.Credentials {
		return orders.ErrStale
	}
	if len(preview.Validation) > 0 {
		return nil
	}
	if config.Finance == nil || config.Finance.OwnerID != owner || !config.Finance.Active(now, config.Scope, config.Revision) {
		return orders.ErrBlocked
	}
	return validateCommissionPlanCurrent(ctx, q, plan, config)
}

func validateCommissionPlanCurrent(ctx context.Context, q ordersQuerier, plan orders.CommissionPlan, config orders.Config) error {
	if plan.Validate() != nil {
		return orders.ErrInvalid
	}
	mappings, err := readOrderProductMappings(ctx, q, config.Scope)
	if err != nil {
		return err
	}
	current, issues := ordermoney.PlanCommissionDraft(config, plan.Credentials, plan.Source, mappings, plan.References, plan.ID, plan.CreatedAt)
	if len(issues) != 0 || current.Fingerprint != plan.Fingerprint {
		return orders.ErrStale
	}
	return nil
}

func observeCommissionSource(ctx context.Context, tx pgx.Tx, source orders.RealizationReport) error {
	source.Analysis = nil
	raw, err := ordersJSON(source)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(source.Scope), source.Period.Year, source.Period.Month, source.Fingerprint, raw)
	_, err = tx.Exec(ctx, `INSERT INTO orders_finance_observations(tenant_id,moysklad_account,ozon_account,report_year,report_month,fingerprint,source) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,moysklad_account,ozon_account,report_year,report_month,fingerprint) DO NOTHING`, args...)
	if err != nil {
		return ordersError("journal authoritative commission source", err)
	}
	if err = persistFinancialLedger(ctx, tx, source); err != nil {
		return err
	}
	if err = persistFinancialEvidence(ctx, tx, source); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_finance_periods SET current_fingerprint=$6,source_changed=source_changed OR source_fingerprint<>$6 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND report_year=$4 AND report_month=$5`, args[:6]...)
	return ordersRowsError("record commission correction diagnostic", err)
}

func insertCommissionPreview(ctx context.Context, tx pgx.Tx, preview orders.CommissionPreview, owner string) error {
	raw, err := ordersJSON(preview)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(preview.Plan.Scope), preview.ID, preview.Plan.ConfigRevision, raw, preview.Actor, owner, preview.Automatic, preview.Plan.CreatedAt, preview.Plan.ExpiresAt)
	_, err = tx.Exec(ctx, `INSERT INTO orders_finance_previews(tenant_id,moysklad_account,ozon_account,id,config_revision,preview,actor,owner_user_id,automatic,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, args...)
	return ordersRowsError("seal commission review", err)
}
