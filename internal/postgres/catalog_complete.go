package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func (s *Store) CompleteCatalogWork(ctx context.Context, c catalogsync.Completion) error {
	if c.Scope.Validate() != nil || c.WorkID == "" || c.ClaimToken == "" || len(c.Code) > 128 {
		return catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin catalog completion: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	w, err := lockCatalogWork(ctx, tx, c.Scope, c.WorkID, c.ClaimToken)
	if err != nil {
		return err
	}
	if w.State != c.ExpectedState {
		return catalogsync.ErrStale
	}
	if err = validateCatalogCompletion(w, c); err != nil {
		return err
	}
	now, err := catalogClock(ctx, tx, c.Now)
	if err != nil {
		return err
	}
	if err = completeCatalogLocked(ctx, tx, w, c, now); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit catalog completion: %w", err)
	}
	return nil
}

func completeCatalogLocked(ctx context.Context, tx pgx.Tx, w catalogsync.Work, c catalogsync.Completion, now time.Time) error {
	var err error
	w.State = c.State
	w.Code = c.Code
	w.NextRunAt = c.NextRunAt
	if c.Receipt.State != "" {
		w.Receipt = sanitizeCatalogReceipt(c.Receipt)
	}
	if c.CreatedTarget != nil {
		w.CreatedTarget = c.CreatedTarget
	}
	if err = writeCatalogWork(ctx, tx, w, now); err != nil {
		return err
	}
	if err = insertCatalogAttempt(ctx, tx, w, c.Code, now); err != nil {
		return err
	}
	if err = saveCatalogCompletionEvidence(ctx, tx, w, c, now); err != nil {
		return err
	}
	if c.State == catalogsync.StateReconciled {
		if err = rebaseCatalogSiblings(ctx, tx, w, c, now); err != nil {
			return err
		}
	}
	if err = finishCatalogTask(ctx, tx, w, now); err != nil {
		return err
	}
	return nil
}

func validateCatalogCompletion(w catalogsync.Work, c catalogsync.Completion) error {
	switch c.State {
	case catalogsync.StateProcessing, catalogsync.StateAcknowledged, catalogsync.StateReconciled, catalogsync.StateRejected, catalogsync.StateUnknown, catalogsync.StateBlocked:
	default:
		return catalogsync.ErrInvalid
	}
	if err := validateCatalogReceipt(w, c.Receipt); err != nil {
		return err
	}
	if c.State == catalogsync.StateProcessing && c.Receipt.TaskID == "" && w.Receipt.TaskID == "" {
		return catalogsync.ErrInvalid
	}
	if c.CreatedTarget != nil && (c.CreatedTarget.Validate() != nil || c.CreatedTarget.Account != w.Command.Target.Account || w.Command.Action != catalog.ActionCreate) {
		return catalogsync.ErrInvalid
	}
	if c.State == catalogsync.StateReconciled {
		return validateCatalogReconciliation(w, c)
	}
	return nil
}

func validateCatalogReceipt(w catalogsync.Work, r catalog.Receipt) error {
	if r.State == "" {
		return nil
	}
	if r.CommandID != w.Command.ID || len(r.Products) > 1 || len(r.Errors) > 100 || len(r.TaskID) > 256 || len(r.ExternalID) > 256 {
		return catalogsync.ErrStale
	}
	if r.HTTPStatus != 0 && (r.HTTPStatus < 100 || r.HTTPStatus > 599) {
		return catalogsync.ErrInvalid
	}
	if w.Receipt.TaskID != "" && r.TaskID != "" && w.Receipt.TaskID != r.TaskID {
		return catalogsync.ErrStale
	}
	if w.Command.Action != catalog.ActionCreate && r.ExternalID != "" && r.ExternalID != w.Command.Target.ID {
		return catalogsync.ErrStale
	}
	for _, p := range r.Products {
		if len(p.Errors) > 100 || (p.ID != "" && r.ExternalID != "" && p.ID != r.ExternalID) {
			return catalogsync.ErrStale
		}
		if w.Command.Action != catalog.ActionCreate && p.ID != "" && p.ID != w.Command.Target.ID {
			return catalogsync.ErrStale
		}
		if p.OfferID != "" && w.Command.Target.OfferID != "" && p.OfferID != w.Command.Target.OfferID {
			return catalogsync.ErrStale
		}
	}
	return nil
}

func validateCatalogReconciliation(w catalogsync.Work, c catalogsync.Completion) error {
	if c.Baseline == nil || c.ObservedSource == nil || c.ObservedTarget == nil {
		return catalogsync.ErrInvalid
	}
	if !w.Decision.Source.Identity.SameEntity(c.ObservedSource.Identity) {
		return catalogsync.ErrStale
	}
	if err := validateCatalogReadback(w, c); err != nil {
		return err
	}
	baseline, err := catalog.NewBaseline(*c.ObservedSource, *c.ObservedTarget)
	if err != nil {
		return err
	}
	actualHash, err := catalogHash(baseline)
	if err != nil {
		return err
	}
	expectedHash, err := catalogHash(c.Baseline)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return catalogsync.ErrStale
	}
	return nil
}

func validateCatalogReadback(w catalogsync.Work, c catalogsync.Completion) error {
	target := w.Decision.Target.Identity
	if w.Command.Action == catalog.ActionCreate {
		created := c.CreatedTarget
		if created == nil {
			created = w.CreatedTarget
		}
		if created == nil {
			return catalogsync.ErrInvalid
		}
		target = *created
	}
	if !target.SameEntity(c.ObservedTarget.Identity) {
		return catalogsync.ErrStale
	}
	observed := c.ObservedTarget
	if w.Command.Target.Account == c.ObservedSource.Identity.Account {
		observed = c.ObservedSource
	}
	return validateCatalogObservedValues(w.Command, *observed)
}

func validateCatalogObservedValues(cmd catalog.Command, observed catalog.Product) error {
	if !catalogsync.ReadbackMatches(cmd, observed) {
		return catalogsync.ErrStale
	}
	return nil
}

func saveCatalogCompletionEvidence(ctx context.Context, tx pgx.Tx, w catalogsync.Work, c catalogsync.Completion, now time.Time) error {
	if c.Receipt.State != "" {
		raw, err := catalogJSON(w.Receipt)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_receipts(work_id,tenant_id,moysklad_account,ozon_account,receipt,task_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, w.ID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, raw, w.Receipt.TaskID, now); err != nil {
			return fmt.Errorf("save catalog provider receipt: %w", err)
		}
	}
	if w.CreatedTarget != nil {
		if err := resolveCatalogAssociation(ctx, tx, w, now); err != nil {
			return err
		}
	}
	if c.State != catalogsync.StateReconciled {
		return nil
	}
	raw, err := catalogJSON(c.Baseline)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_baselines(work_id,tenant_id,moysklad_account,ozon_account,target_key,baseline,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, w.ID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, w.EnrollmentID, raw, now); err != nil {
		return fmt.Errorf("save reconciled catalog baseline: %w", err)
	}
	for _, p := range []*catalog.Product{c.ObservedSource, c.ObservedTarget} {
		if _, err = insertCatalogSnapshot(ctx, tx, w.Scope, *p); err != nil {
			return err
		}
	}
	return nil
}

func resolveCatalogAssociation(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	raw, err := catalogJSON(w.CreatedTarget)
	if err != nil {
		return err
	}
	args := append(catalogScopeArgs(w.Scope), w.EnrollmentID, raw, now)
	tag, err := tx.Exec(ctx, `UPDATE catalog_sync_associations SET resolved_identity=$5,resolved_at=COALESCE(resolved_at,$6) WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND target_key=$4 AND provisional AND (resolved_identity IS NULL OR resolved_identity=$5::jsonb)`, args...)
	if err != nil {
		return fmt.Errorf("persist created catalog identity: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return catalogsync.ErrStale
	}
	return nil
}

func finishCatalogTask(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	state := "pending"
	if catalogTerminal(w.State) {
		state = "failed"
	}
	if w.State == catalogsync.StateReconciled {
		state = "succeeded"
	}
	next := w.NextRunAt
	if next.Before(now) {
		next = now
	}
	_, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state=$2,available_at=$3,claim_token=NULL,lease_until=NULL,last_error=NULLIF($4,''),updated_at=$5 WHERE task_type='catalog_sync' AND catalog_work_id=$1`, w.ID, state, next, w.Code, now)
	if err != nil {
		return fmt.Errorf("finish catalog outbox phase: %w", err)
	}
	return nil
}

func sanitizeCatalogReceipt(r catalog.Receipt) catalog.Receipt {
	for i := range r.Errors {
		r.Errors[i].Message = ""
	}
	for i := range r.Products {
		for j := range r.Products[i].Errors {
			r.Products[i].Errors[j].Message = ""
		}
	}
	return r
}
