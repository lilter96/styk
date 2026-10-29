package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
)

func (s *Store) GuardCatalogDispatch(ctx context.Context, g catalogsync.DispatchGuard) (bool, error) {
	if g.Work.Scope.Validate() != nil || g.Work.ID == "" || g.Work.ClaimToken == "" {
		return false, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin catalog dispatch guard: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockCatalogDispatchScope(ctx, tx, g.Work.Scope); err != nil {
		return false, err
	}
	w, err := lockCatalogWork(ctx, tx, g.Work.Scope, g.Work.ID, g.Work.ClaimToken)
	if err != nil {
		return false, err
	}
	valid, err := catalogDispatchCurrent(ctx, tx, w, g)
	if err != nil || !valid {
		return false, err
	}
	now, err := catalogClock(ctx, tx, g.Now)
	if err != nil {
		return false, err
	}
	if g.Begin {
		if err = beginCatalogDispatch(ctx, tx, w, now); err != nil {
			return false, err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit catalog dispatch guard: %w", err)
	}
	return true, nil
}

func lockCatalogDispatchScope(ctx context.Context, tx pgx.Tx, scope catalog.Scope) error {
	if err := catalogLock(ctx, tx, scope); err != nil {
		return err
	}
	if err := catalogCredentialLocks(ctx, tx, scope); err != nil {
		return err
	}
	return catalogMappingLocks(ctx, tx, scope)
}

func beginCatalogDispatch(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	w.State = catalogsync.StateDispatching
	raw, err := catalogJSON(w)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE catalog_sync_work SET state='DISPATCHING',work=$2,dispatch_count=1,dispatched_at=$3,updated_at=$3 WHERE id=$1`, w.ID, raw, now); err != nil {
		return fmt.Errorf("fence catalog dispatch intent: %w", err)
	}
	if err = insertCatalogDispatchIntent(ctx, tx, w, now); err != nil {
		return err
	}
	return insertCatalogAttempt(ctx, tx, w, "", now)
}

func catalogMappingLocks(ctx context.Context, tx pgx.Tx, scope catalog.Scope) error {
	stock := domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	for _, key := range stockReviewLockKeys(stock) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return fmt.Errorf("lock catalog mapping evidence: %w", err)
		}
	}
	return nil
}

func catalogDispatchCurrent(ctx context.Context, tx pgx.Tx, w catalogsync.Work, g catalogsync.DispatchGuard) (bool, error) {
	allowedState := w.State == catalogsync.StateClaimed || (!g.Begin && w.State == catalogsync.StateDispatching)
	hash, err := catalog.CommandFingerprint(g.Work.Command)
	if err != nil {
		return false, err
	}
	if !allowedState || hash != w.CommandFingerprint || g.Work.CommandFingerprint != w.CommandFingerprint || g.Work.ConfigRevision != w.ConfigRevision || g.Work.Credentials != w.Credentials {
		return false, nil
	}
	current, err := readCatalogReviewContext(ctx, tx, w.Scope)
	if errors.Is(err, catalogsync.ErrStale) || errors.Is(err, catalogsync.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current.Config.Enabled || !current.Config.TestEnabled || !catalogTestScope(w.Scope) || current.Config.Revision != w.ConfigRevision || current.Credentials != w.Credentials {
		return false, nil
	}
	for _, t := range current.Targets {
		if t.Enrollment.ID != w.EnrollmentID {
			continue
		}
		if t.Mapping != nil && (w.Decision.Mapping == nil || *w.Decision.Mapping != *t.Mapping) {
			return false, nil
		}
		if w.Command.Action == catalog.ActionCreate {
			return catalogCreationDispatchable(ctx, tx, w.Scope, t)
		}
		if w.Automatic && !catalogAutomaticDecision(t.Enrollment, w.Decision) {
			return false, nil
		}
		return true, nil
	}
	return false, nil
}

func insertCatalogDispatchIntent(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	raw, err := catalogJSON(w.Command)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO catalog_sync_dispatch_intents(work_id,tenant_id,moysklad_account,ozon_account,command,fingerprint,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, w.ID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, raw, w.CommandFingerprint, now)
	if err != nil {
		return fmt.Errorf("journal exact catalog dispatch intent: %w", err)
	}
	return nil
}

func catalogCreationDispatchable(ctx context.Context, tx pgx.Tx, scope catalog.Scope, t catalogsync.TargetContext) (bool, error) {
	if t.CreatedTarget != nil {
		return false, nil
	}
	err := validateCatalogCreations(ctx, tx, scope, []catalogsync.Enrollment{t.Enrollment})
	if errors.Is(err, catalogsync.ErrStale) {
		return false, nil
	}
	return err == nil, err
}
