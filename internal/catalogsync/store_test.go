package catalogsync

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// memoryStore models durable boundary behavior with JSON copies. Production
// transaction, SQL isolation and lease concurrency tests belong to postgres.
type memoryStore struct {
	current        ReviewContext
	previews       map[string]Preview
	approvals      map[string]Operation
	works          []Work
	completions    []Completion
	claims         int
	beginCount     int
	due            bool
	failCompletion bool
	dueLimit       int
}

func (store *memoryStore) CatalogConfig(_ context.Context, scope catalog.Scope) (Config, error) {
	if scope != store.current.Config.Scope {
		return Config{}, ErrNotFound
	}
	return clone(store.current.Config), nil
}

func (store *memoryStore) SaveCatalogConfig(_ context.Context, request SaveConfigRequest, now time.Time) (Config, error) {
	if request.ExpectedRevision != store.current.Config.Revision {
		return Config{}, ErrStale
	}
	request.Config.Revision, request.Config.UpdatedAt = request.ExpectedRevision+1, now
	store.current.Config = clone(request.Config)
	return clone(request.Config), nil
}

func (store *memoryStore) DueCatalogConfigs(_ context.Context, _ time.Time, limit int) ([]Config, error) {
	store.dueLimit = limit
	if !store.due {
		return nil, nil
	}
	store.due = false
	return []Config{clone(store.current.Config)}, nil
}

func (store *memoryStore) CatalogReviewContext(_ context.Context, scope catalog.Scope) (ReviewContext, error) {
	if scope != store.current.Config.Scope {
		return ReviewContext{}, ErrNotFound
	}
	return clone(store.current), nil
}

func (store *memoryStore) SaveCatalogPreview(_ context.Context, preview Preview) (Preview, error) {
	saved := clone(preview)
	saved.Actor = preview.Actor
	store.previews[preview.ID] = saved
	return preview, nil
}

func (store *memoryStore) CatalogPreview(_ context.Context, scope catalog.Scope, id string) (Preview, error) {
	preview, ok := store.previews[id]
	if !ok || preview.Scope != scope {
		return Preview{}, ErrNotFound
	}
	result := clone(preview)
	result.Actor = preview.Actor
	return result, nil
}

func (store *memoryStore) ApproveCatalogPreview(_ context.Context, approval Approval, now time.Time) (Operation, error) {
	if previous, ok := store.approvals[approval.IdempotencyKey]; ok {
		if previous.PreviewID != approval.PreviewID {
			return Operation{}, ErrConflict
		}
		return clone(previous), nil
	}
	preview, ok := store.previews[approval.PreviewID]
	if !ok || preview.Scope != approval.Scope || preview.ConfigRevision != store.current.Config.Revision || preview.Credentials != store.current.Credentials {
		return Operation{}, ErrStale
	}
	if err := preview.approvable(approval, now); err != nil {
		return Operation{}, err
	}
	operation := Operation{ID: "operation-" + preview.ID, Scope: approval.Scope, PreviewID: preview.ID, State: StateQueued, CreatedAt: now}
	for _, decision := range preview.Decisions {
		if !selectedDecision(approval, decision.EnrollmentID) {
			continue
		}
		for _, command := range decision.Commands {
			fingerprint, err := catalog.CommandFingerprint(command)
			if err != nil {
				return Operation{}, err
			}
			operation.Targets = append(operation.Targets, Work{ID: command.ID, OperationID: operation.ID, Scope: preview.Scope,
				EnrollmentID: decision.EnrollmentID, ConfigRevision: preview.ConfigRevision, Credentials: preview.Credentials,
				Command: command, CommandFingerprint: fingerprint, Decision: decision, State: StateQueued, NextRunAt: now})
		}
	}
	store.works = append(store.works, clone(operation.Targets)...)
	store.approvals[approval.IdempotencyKey] = clone(operation)
	return clone(operation), nil
}

func selectedDecision(approval Approval, id string) bool {
	if len(approval.EnrollmentIDs) == 0 {
		return true
	}
	for _, selected := range approval.EnrollmentIDs {
		if selected == id {
			return true
		}
	}
	return false
}

func (store *memoryStore) CatalogProducts(context.Context, catalog.Scope, PageRequest) (ProductPage, error) {
	return ProductPage{Products: make([]catalog.Product, 0)}, nil
}

func (store *memoryStore) CatalogHistory(context.Context, catalog.Scope, PageRequest) (HistoryPage, error) {
	return HistoryPage{Operations: make([]Operation, 0)}, nil
}

func (store *memoryStore) ClaimCatalogWork(_ context.Context, request ClaimRequest) (*Work, error) {
	for i := range store.works {
		work := &store.works[i]
		if request.Now.Before(work.NextRunAt) || store.siblingPending(i) {
			continue
		}
		switch work.State {
		case StateQueued:
			work.State = StateClaimed
		case StateDispatching:
			work.State = StateUnknown
		case StateClaimed, StateProcessing, StateAcknowledged, StateUnknown:
		default:
			continue
		}
		store.claims++
		work.ClaimToken = fmt.Sprintf("claim-%d", store.claims)
		work.NextRunAt = request.LeaseUntil
		result := clone(*work)
		result.ClaimToken = work.ClaimToken
		return &result, nil
	}
	return nil, nil
}

func (store *memoryStore) siblingPending(index int) bool {
	work := store.works[index]
	for i := 0; i < index; i++ {
		sibling := store.works[i]
		terminal := sibling.State == StateReconciled || sibling.State == StateRejected || sibling.State == StateBlocked
		if sibling.OperationID == work.OperationID && sibling.EnrollmentID == work.EnrollmentID && !terminal {
			return true
		}
	}
	return false
}

func (store *memoryStore) GuardCatalogDispatch(_ context.Context, guard DispatchGuard) (bool, error) {
	config := store.current.Config
	if !config.Enabled || !config.TestEnabled || guard.Work.ConfigRevision != config.Revision || guard.Work.Credentials != store.current.Credentials {
		return false, nil
	}
	for _, target := range store.current.Targets {
		if target.Enrollment.ID == guard.Work.EnrollmentID && target.Enrollment.Create == nil && !mappingMatches(target) {
			return false, nil
		}
	}
	for i := range store.works {
		work := &store.works[i]
		if work.ID != guard.Work.ID {
			continue
		}
		if work.ClaimToken != guard.Work.ClaimToken || work.CommandFingerprint != guard.Work.CommandFingerprint {
			return false, nil
		}
		if guard.Begin && work.State == StateClaimed {
			work.State = StateDispatching
			store.beginCount++
			return true, nil
		}
		return !guard.Begin && (work.State == StateClaimed || work.State == StateDispatching), nil
	}
	return false, ErrNotFound
}

func (store *memoryStore) CompleteCatalogWork(_ context.Context, completion Completion) error {
	if store.failCompletion {
		return ErrConflict
	}
	for i := range store.works {
		work := &store.works[i]
		if work.ID != completion.WorkID {
			continue
		}
		if work.ClaimToken != completion.ClaimToken || work.State != completion.ExpectedState {
			return ErrStale
		}
		work.State, work.Code, work.Receipt, work.NextRunAt = completion.State, completion.Code, clone(completion.Receipt), completion.NextRunAt
		work.CreatedTarget = completion.CreatedTarget
		store.completions = append(store.completions, clone(completion))
		if completion.State == StateReconciled {
			return store.reconciled(i, completion)
		}
		return nil
	}
	return ErrNotFound
}

func (store *memoryStore) reconciled(index int, completion Completion) error {
	work := store.works[index]
	for i := range store.current.Targets {
		target := &store.current.Targets[i]
		if target.Enrollment.ID == work.EnrollmentID {
			target.Baseline, target.CreatedTarget = clone(completion.Baseline), clone(completion.CreatedTarget)
		}
	}
	for i := index + 1; i < len(store.works); i++ {
		sibling := store.works[i]
		if sibling.EnrollmentID != work.EnrollmentID || sibling.OperationID != work.OperationID {
			continue
		}
		rebased, err := RebaseWork(sibling, work.Command, *completion.ObservedSource, *completion.ObservedTarget)
		if err != nil {
			store.works[i].State = StateBlocked
			continue
		}
		store.works[i] = rebased
	}
	return nil
}
