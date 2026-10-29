package orderflow

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lilter96/styk/internal/orders"
)

// Tick advances bounded ingestion and review independently for every due scope.
// Only a current owner grant can convert ready automatic previews into work.
func (r *Runner) Tick(ctx context.Context) error {
	if r.options.Service == nil || r.options.Collector == nil {
		return orders.ErrBlocked
	}
	configs, err := r.options.Service.store.DueOrderConfigs(ctx, r.now(), 25)
	if err != nil {
		return fmt.Errorf("read due order configurations: %w", err)
	}
	errorsByScope := make([]error, 0)
	for _, config := range configs {
		if err = r.tickScope(ctx, config); err != nil {
			errorsByScope = append(errorsByScope, err)
		}
	}
	return errors.Join(errorsByScope...)
}

func (r *Runner) tickScope(ctx context.Context, config orders.Config) error {
	if !config.Enabled || !config.TestEnabled || r.options.AllowScope == nil || !r.options.AllowScope(config.Scope) {
		return nil
	}
	grant := config.Automation
	if grant != nil && !grant.Active(r.now(), config.Scope, config.Revision) {
		return nil
	}
	if err := r.options.Collector.Collect(ctx, config.Scope); err != nil {
		return fmt.Errorf("collect due order facts: %w", err)
	}
	if grant == nil {
		return nil
	}
	actor := "AUTOMATION:" + grant.ID + ":" + strconv.FormatInt(grant.Revision, 10)
	preview, err := r.options.Service.preview(ctx, orders.PreviewRequest{Scope: config.Scope, ExpectedRevision: config.Revision, Actor: actor}, true)
	if err != nil {
		return fmt.Errorf("review automatic order mirror: %w", err)
	}
	if len(readyPostingNumbers(preview)) == 0 {
		return nil
	}
	_, err = r.options.Service.approve(ctx, orders.Approval{Scope: config.Scope, PreviewID: preview.ID, Actor: actor,
		Automatic: true, IdempotencyKey: "automatic:" + preview.ID})
	if err != nil {
		return fmt.Errorf("approve granted order mirror: %w", err)
	}
	return nil
}
