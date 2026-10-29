package catalogsync

import (
	"context"
	"fmt"
	"time"
)

func (service Service) approve(ctx context.Context, approval Approval) (Operation, error) {
	if err := service.validateScope(approval.Scope); err != nil {
		return Operation{}, err
	}
	if !service.allowed(approval.Scope) || !validID(approval.Actor) || !validID(approval.PreviewID) || !validID(approval.IdempotencyKey) {
		return Operation{}, ErrBlocked
	}
	// The store checks replay before freshness under the same transaction. An
	// already approved idempotency key must survive expiry or later config edits.
	operation, err := service.Store.ApproveCatalogPreview(ctx, approval, service.now())
	if err != nil {
		return Operation{}, fmt.Errorf("approve catalog preview and enqueue intents: %w", err)
	}
	return operation, nil
}

func (preview Preview) approvable(approval Approval, now time.Time) error {
	if preview.Scope != approval.Scope || preview.ID != approval.PreviewID || preview.Actor != approval.Actor || preview.Automatic != approval.Automatic {
		return ErrBlocked
	}
	if !now.Before(preview.ExpiresAt) || now.Before(preview.CreatedAt) {
		return ErrStale
	}
	selected := make(map[string]bool, len(approval.EnrollmentIDs))
	for _, id := range approval.EnrollmentIDs {
		if selected[id] || !validID(id) {
			return ErrInvalid
		}
		selected[id] = true
	}
	count := 0
	for _, decision := range preview.Decisions {
		if len(approval.EnrollmentIDs) != 0 && !selected[decision.EnrollmentID] {
			continue
		}
		if !decision.actionable() {
			return ErrBlocked
		}
		delete(selected, decision.EnrollmentID)
		count++
	}
	if len(selected) != 0 || count == 0 {
		return ErrBlocked
	}
	return nil
}

// Tick schedules bounded, fresh decisions only for previously authorized test
// enrollments. DueCatalogConfigs must atomically advance its scheduling cursor.
func (service Service) Tick(ctx context.Context) (bool, error) {
	if service.Store == nil || service.Resolver == nil {
		return false, ErrBlocked
	}
	configs, err := service.Store.DueCatalogConfigs(ctx, service.now(), 20)
	if err != nil {
		return false, fmt.Errorf("claim due catalog configurations: %w", err)
	}
	if len(configs) > 20 {
		return false, ErrInvalid
	}
	worked := false
	for _, config := range configs {
		if !config.Enabled || !config.TestEnabled || !service.allowed(config.Scope) {
			continue
		}
		didWork, err := service.schedule(ctx, config)
		worked = worked || didWork
		if err != nil {
			return worked, err
		}
	}
	return worked, nil
}

func (service Service) schedule(ctx context.Context, config Config) (bool, error) {
	ids := automaticIDs(config.Targets)
	if len(ids) == 0 {
		return false, nil
	}
	preview, err := service.preview(ctx, PreviewRequest{Scope: config.Scope, ExpectedRevision: config.Revision,
		EnrollmentIDs: ids, Actor: "catalog-scheduler"}, true)
	if err != nil {
		return false, err
	}
	ready := make([]string, 0, len(preview.Decisions))
	for _, decision := range preview.Decisions {
		if decision.actionable() {
			ready = append(ready, decision.EnrollmentID)
		}
	}
	if len(ready) == 0 {
		return true, nil
	}
	_, err = service.approve(ctx, Approval{Scope: config.Scope, PreviewID: preview.ID, EnrollmentIDs: ready,
		Actor: preview.Actor, IdempotencyKey: "automatic:" + preview.ID, Automatic: true})
	return true, err
}

func automaticIDs(targets []Enrollment) []string {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Automatic {
			ids = append(ids, target.ID)
		}
	}
	return ids
}

// AutomaticRunner shares the existing worker loop; it introduces no goroutine.
type AutomaticRunner struct{ Service Service }

func (runner AutomaticRunner) RunNext(ctx context.Context) (bool, error) {
	return runner.Service.Tick(ctx)
}
