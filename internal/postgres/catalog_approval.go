package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func (s *Store) ApproveCatalogPreview(ctx context.Context, a catalogsync.Approval, now time.Time) (catalogsync.Operation, error) {
	var zero catalogsync.Operation
	if a.Scope.Validate() != nil || a.PreviewID == "" || a.Actor == "" || a.IdempotencyKey == "" || len(a.IdempotencyKey) > 128 {
		return zero, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, fmt.Errorf("begin catalog approval: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, a.Scope.Tenant, a.Actor); err != nil {
		return zero, err
	}
	if err = catalogLock(ctx, tx, a.Scope); err != nil {
		return zero, err
	}
	if err = catalogCredentialLocks(ctx, tx, a.Scope); err != nil {
		return zero, err
	}
	op, err := approveCatalogLocked(ctx, tx, a, now)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, catalogError("commit catalog approval", err)
	}
	return op, nil
}

func approveCatalogLocked(ctx context.Context, tx pgx.Tx, a catalogsync.Approval, now time.Time) (catalogsync.Operation, error) {
	var zero catalogsync.Operation
	hash, err := catalogHash(struct {
		Approval  catalogsync.Approval
		Actor     string
		Automatic bool
	}{a, a.Actor, a.Automatic})
	if err != nil {
		return zero, err
	}
	replay, err := replayCatalogApproval(ctx, tx, a, hash)
	if err == nil {
		return replay, nil
	}
	if !errors.Is(err, catalogsync.ErrNotFound) {
		return zero, err
	}
	p, err := readCatalogPreview(ctx, tx, a.Scope, a.PreviewID)
	if err != nil {
		return zero, err
	}
	current, err := readCatalogReviewContext(ctx, tx, a.Scope)
	if err != nil {
		return zero, err
	}
	now, err = catalogClock(ctx, tx, now)
	if err != nil {
		return zero, err
	}
	if err = validateCatalogApproval(a, p, current, now); err != nil {
		return zero, err
	}
	decisions, err := selectedCatalogDecisions(a, p, current.Config)
	if err != nil {
		return zero, err
	}
	return insertCatalogApproval(ctx, tx, a, p, hash, decisions, now)
}

func replayCatalogApproval(ctx context.Context, tx pgx.Tx, a catalogsync.Approval, hash string) (catalogsync.Operation, error) {
	var id, previous string
	args := append(catalogScopeArgs(a.Scope), a.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT o.id,a.request_hash FROM catalog_sync_approvals a JOIN catalog_sync_operations o ON o.approval_id=a.id WHERE a.tenant_id=$1 AND a.moysklad_account=$2 AND a.ozon_account=$3 AND a.operator_key=$4`, args...).Scan(&id, &previous)
	if err != nil {
		return catalogsync.Operation{}, catalogError("read catalog approval replay", err)
	}
	if hash != previous {
		return catalogsync.Operation{}, catalogsync.ErrConflict
	}
	return readCatalogOperation(ctx, tx, a.Scope, id)
}

func validateCatalogApproval(a catalogsync.Approval, p catalogsync.Preview, current catalogsync.ReviewContext, now time.Time) error {
	c := current.Config
	if !c.Enabled || !c.TestEnabled || !catalogTestScope(a.Scope) {
		return catalogsync.ErrBlocked
	}
	if !p.ExpiresAt.After(now) || p.ConfigRevision != c.Revision || p.Credentials != current.Credentials || p.Credentials.Moysklad < 1 || p.Credentials.Ozon < 1 {
		return catalogsync.ErrStale
	}
	if p.Actor != a.Actor || p.Automatic != a.Automatic {
		return catalogsync.ErrBlocked
	}
	if err := validateCatalogPreview(p, current); err != nil {
		return err
	}
	return nil
}

func selectedCatalogDecisions(a catalogsync.Approval, p catalogsync.Preview, c catalogsync.Config) ([]catalogsync.Decision, error) {
	selected := make(map[string]bool, len(a.EnrollmentIDs))
	targets := make(map[string]catalogsync.Enrollment, len(c.Targets))
	for _, id := range a.EnrollmentIDs {
		if selected[id] {
			return nil, catalogsync.ErrInvalid
		}
		selected[id] = true
	}
	for _, e := range c.Targets {
		targets[e.ID] = e
	}
	decisions := make([]catalogsync.Decision, 0, len(p.Decisions))
	for _, d := range p.Decisions {
		if len(a.EnrollmentIDs) > 0 && !selected[d.EnrollmentID] {
			continue
		}
		delete(selected, d.EnrollmentID)
		if !d.Ready || len(d.Validation) > 0 || len(d.Plan.Conflicts) > 0 {
			return nil, catalogsync.ErrBlocked
		}
		if len(d.Commands) == 0 {
			continue
		}
		if a.Automatic && !catalogAutomaticDecision(targets[d.EnrollmentID], d) {
			return nil, catalogsync.ErrBlocked
		}
		decisions = append(decisions, d)
	}
	if len(selected) > 0 {
		return nil, catalogsync.ErrInvalid
	}
	if len(decisions) == 0 {
		return nil, catalogsync.ErrBlocked
	}
	return decisions, nil
}

func catalogAutomaticDecision(e catalogsync.Enrollment, d catalogsync.Decision) bool {
	if !e.Automatic || e.Create != nil {
		return false
	}
	allowed := make(map[catalog.Field]bool, len(e.AutomaticFields))
	for _, field := range e.AutomaticFields {
		allowed[field] = true
	}
	for _, cmd := range d.Commands {
		if cmd.Action != catalog.ActionUpdate || len(cmd.Extensions) > 0 {
			return false
		}
		for field := range cmd.Values {
			if !allowed[field] {
				return false
			}
		}
	}
	return true
}

func insertCatalogApproval(ctx context.Context, tx pgx.Tx, a catalogsync.Approval, p catalogsync.Preview, hash string, decisions []catalogsync.Decision, now time.Time) (catalogsync.Operation, error) {
	id, err := catalogID()
	if err != nil {
		return catalogsync.Operation{}, err
	}
	op := catalogsync.Operation{ID: id, Scope: a.Scope, PreviewID: p.ID, State: catalogsync.StateQueued, CreatedAt: now, Targets: make([]catalogsync.Work, 0, len(decisions))}
	args := []any{id, a.Scope.Tenant, a.Scope.MoyskladAccount, a.Scope.OzonAccount, p.ConfigRevision, p.ID, a.Actor, a.IdempotencyKey, hash, now}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_approvals(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,operator_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, args...); err != nil {
		return op, catalogError("save catalog approval audit", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,approval_id,preview_id,created_at) VALUES($1,$2,$3,$4,$5,$1,$6,$7)`, id, a.Scope.Tenant, a.Scope.MoyskladAccount, a.Scope.OzonAccount, p.ConfigRevision, p.ID, now); err != nil {
		return op, fmt.Errorf("save catalog operation: %w", err)
	}
	for _, d := range decisions {
		for _, cmd := range d.Commands {
			work, err := insertCatalogWork(ctx, tx, op, p, d, cmd, a.Automatic, now)
			if err != nil {
				return op, err
			}
			op.Targets = append(op.Targets, work)
		}
	}
	return op, nil
}

func insertCatalogWork(ctx context.Context, tx pgx.Tx, op catalogsync.Operation, p catalogsync.Preview, d catalogsync.Decision, cmd catalog.Command, automatic bool, now time.Time) (catalogsync.Work, error) {
	id, err := catalogID()
	if err != nil {
		return catalogsync.Work{}, err
	}
	hash, err := catalog.CommandFingerprint(cmd)
	if err != nil {
		return catalogsync.Work{}, err
	}
	w := catalogsync.Work{ID: id, OperationID: op.ID, Scope: op.Scope, EnrollmentID: d.EnrollmentID, ConfigRevision: p.ConfigRevision, Credentials: p.Credentials, Command: cmd, CommandFingerprint: hash, Decision: d, State: catalogsync.StateQueued, Automatic: automatic, NextRunAt: now}
	raw, err := catalogJSON(w)
	if err != nil {
		return w, err
	}
	target := catalogTargetIdentity(cmd)
	args := []any{id, op.ID, op.Scope.Tenant, op.Scope.MoyskladAccount, op.Scope.OzonAccount, p.ConfigRevision, d.EnrollmentID, cmd.Target.Account.Provider, cmd.Target.Account.ID, target, w.State, raw, now}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,target_key,target_provider,target_account,target_identity,state,work,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`, args...); err != nil {
		return w, catalogError("fence catalog target", err)
	}
	commandRaw, err := catalogJSON(cmd)
	if err != nil {
		return w, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_commands(work_id,tenant_id,moysklad_account,ozon_account,command_id,command) VALUES($1,$2,$3,$4,$5,$6)`, id, op.Scope.Tenant, op.Scope.MoyskladAccount, op.Scope.OzonAccount, cmd.ID, commandRaw); err != nil {
		return w, catalogError("journal approved catalog command", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,task_type,catalog_operation_id,catalog_work_id,catalog_tenant_id,catalog_moysklad_account,catalog_ozon_account,max_attempts,available_at) VALUES($1,'catalog_sync',$2,$3,$4,$5,$6,1,$7)`, "catalog-sync:"+id, op.ID, id, op.Scope.Tenant, op.Scope.MoyskladAccount, op.Scope.OzonAccount, now); err != nil {
		return w, fmt.Errorf("enqueue catalog command: %w", err)
	}
	return w, nil
}

func catalogTargetIdentity(cmd catalog.Command) string {
	if cmd.Target.ID != "" {
		return "entity:" + cmd.Target.EntityType + ":" + cmd.Target.ID
	}
	return "create:" + string(cmd.Source.Account.Provider) + ":" + cmd.Source.EntityType + ":" + cmd.Source.ID
}
