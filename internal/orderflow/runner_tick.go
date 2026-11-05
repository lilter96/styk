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

type sourceRefreshRecorder interface {
	RecordOrderSourceRefresh(context.Context, orders.Scope, error) error
}

func (r *Runner) tickScope(ctx context.Context, config orders.Config) error {
	writeAllowed := config.Enabled && config.TestEnabled && r.options.AllowScope != nil && r.options.AllowScope(config.Scope)
	if !config.ReadsEnabled() || (!config.ReadEnabled && !writeAllowed) {
		return nil
	}
	grant := config.Automation
	if !config.ReadEnabled && grant != nil && !grant.Active(r.now(), config.Scope, config.Revision) {
		return nil
	}
	collectErr := r.options.Collector.Collect(ctx, config.Scope)
	if tracker, ok := r.options.Service.store.(sourceRefreshRecorder); ok {
		collectErr = errors.Join(collectErr, tracker.RecordOrderSourceRefresh(ctx, config.Scope, collectErr))
	}
	if collectErr != nil {
		collectErr = fmt.Errorf("collect due order facts: %w", collectErr)
	}
	if !writeAllowed || grant == nil || !grant.Active(r.now(), config.Scope, config.Revision) || ctx.Err() != nil {
		return collectErr
	}
	// A partial read must not hide other already-fresh targets. Every selected
	// decision retains source freshness checks and a fresh pre-dispatch read.
	return errors.Join(collectErr, r.reviewDueScope(ctx, config, grant))
}

func (r *Runner) reviewDueScope(ctx context.Context, config orders.Config, grant *orders.AutomationGrant) error {
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
