package orderflow

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type commissionScheduleReader interface {
	DueCommissionConfigs(context.Context, time.Time, int) ([]orders.Config, error)
}

func (r *CommissionRunner) Tick(ctx context.Context) error {
	store, ok := r.options.Service.store.(commissionScheduleReader)
	if !ok {
		return orders.ErrBlocked
	}
	configs, err := store.DueCommissionConfigs(ctx, r.now(), 25)
	if err != nil {
		return err
	}
	failures := make([]error, 0, len(configs))
	for _, config := range configs {
		if err = r.options.Service.ProcessCommissionPeriod(ctx, config); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// ProcessCommissionPeriod enrolls only the previous closed month, from day five.
// The independent finance policy supplies authority; mirror grants are unrelated.
func (s *Service) ProcessCommissionPeriod(ctx context.Context, config orders.Config) error {
	now, policy := s.now(), config.Finance
	if now.Day() < 5 || policy == nil || !policy.Enabled || !policy.Automatic || !s.allowed(config.Scope) {
		return nil
	}
	previous := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	period := orders.RealizationPeriod{Year: previous.Year(), Month: int(previous.Month())}
	actor := "FINANCE:" + policy.ID + ":" + strconv.FormatInt(policy.Revision, 10)
	preview, err := s.previewCommission(ctx, CommissionPreviewRequest{Scope: config.Scope, ExpectedRevision: config.Revision, Period: period, Actor: actor}, true)
	if err != nil {
		return err
	}
	if len(preview.Validation) != 0 || preview.ExistingWork != nil || preview.SourceChanged {
		return nil
	}
	_, err = s.approveCommission(ctx, orders.CommissionApproval{Scope: config.Scope, PreviewID: preview.ID, Actor: actor, Automatic: true, IdempotencyKey: "finance:auto:" + config.Scope.OzonAccount + ":" + strconv.Itoa(period.Year) + ":" + strconv.Itoa(period.Month)})
	return err
}
