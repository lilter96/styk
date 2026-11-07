package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

type ProductMatchingApproval struct {
	Scope             catalog.Scope
	JobID, Actor, Key string
	Expected          int
	Acknowledge       bool
}

func (s *Store) ApplyProductMatchingJob(ctx context.Context, r ProductMatchingApproval) (ProductMatchingApplication, error) {
	if r.Scope.Validate() != nil || !r.Acknowledge || r.Expected <= 0 || !importKeyPattern.MatchString(r.Key) {
		return ProductMatchingApplication{}, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if err := s.queueProductMatchingApplication(ctx, r); err != nil {
		return ProductMatchingApplication{}, err
	}
	return s.executeProductMatchingApplication(ctx, r.JobID)
}
func (s *Store) queueProductMatchingApplication(ctx context.Context, r ProductMatchingApproval) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackMerchantMutation(tx)
	if err = queueMatchingApproval(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type matchingApprovalState struct {
	state, apply, key string
	count, total      int
}

func queueMatchingApproval(ctx context.Context, tx pgx.Tx, r ProductMatchingApproval) error {
	session, err := catalogActorSession(ctx, r.Actor)
	if err != nil {
		return err
	}
	batch := &pgx.Batch{}
	args := append(catalogScopeArgs(r.Scope), r.JobID, r.Actor)
	batch.Queue(`SELECT state,apply_state,apply_key,COALESCE((summary->>'exact')::int,0),apply_total FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5 FOR UPDATE`, args...)
	if session != "" {
		batch.Queue(`SELECT saas_require_actor($1,$2,true)`, r.Scope.Tenant, session)
	}
	batch.Queue(`UPDATE product_matching_jobs SET apply_state='QUEUED',apply_key=$6,apply_total=$7,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5 AND apply_state='NOT_STARTED' AND state='COMPLETED' AND COALESCE((summary->>'exact')::int,0)=$7`, append(args, r.Key, r.Expected)...)
	results := tx.SendBatch(ctx, batch)
	var previous matchingApprovalState
	err = results.QueryRow().Scan(&previous.state, &previous.apply, &previous.key, &previous.count, &previous.total)
	if err != nil {
		return catalogError("read matching approval", errors.Join(err, results.Close()))
	}
	for i := 1; i < batch.Len(); i++ {
		if _, err = results.Exec(); err != nil {
			return errors.Join(err, results.Close())
		}
	}
	if err = results.Close(); err != nil {
		return err
	}
	return previous.validate(r)
}

func (previous matchingApprovalState) validate(r ProductMatchingApproval) error {
	if previous.apply != "NOT_STARTED" {
		if previous.key != r.Key || previous.total != r.Expected {
			return catalogsync.ErrConflict
		}
		return nil
	}
	if previous.state != "COMPLETED" || previous.count != r.Expected {
		return catalogsync.ErrStale
	}
	return nil
}

func (s *Store) executeProductMatchingApplication(ctx context.Context, id string) (ProductMatchingApplication, error) {
	tx, err := beginProductMatchingSet(ctx, s.Pool)
	if err != nil {
		return ProductMatchingApplication{}, err
	}
	defer rollbackMerchantMutation(tx)
	w, app, err := readProductMatchingApplication(ctx, tx, id)
	if err != nil {
		return app, err
	}
	if app.State != "QUEUED" && app.State != "RUNNING" {
		return app, tx.Commit(ctx)
	}
	result, err := authorizeAndApplyMatching(ctx, tx, w)
	if err != nil {
		result, err = failProductMatchingApplication(ctx, tx, id, app, err)
		if err != nil {
			return app, err
		}
	}
	return result, tx.Commit(ctx)
}

func failProductMatchingApplication(ctx context.Context, tx pgx.Tx, id string, app ProductMatchingApplication, cause error) (ProductMatchingApplication, error) {
	if ctx.Err() != nil {
		return app, cause
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT matching_apply`); err != nil {
		return app, errors.Join(cause, err)
	}
	app.State, app.Code = "FAILED", "MATCHING_SERVICE_UNAVAILABLE"
	var sqlError *pgconn.PgError
	if errors.Is(cause, catalogsync.ErrStale) || errors.Is(cause, catalogsync.ErrBlocked) || (errors.As(cause, &sqlError) && sqlError.Code == "42501") {
		app.State, app.Code = "STALE", "MATCHING_SOURCE_STALE"
	}
	_, err := tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state=$2,apply_code=$3,updated_at=clock_timestamp() WHERE id=$1`, id, app.State, app.Code)
	return app, err
}

func readProductMatchingApplication(ctx context.Context, tx pgx.Tx, id string) (productMatchingWork, ProductMatchingApplication, error) {
	var w productMatchingWork
	var app ProductMatchingApplication
	batch := &pgx.Batch{}
	batch.Queue(`SELECT id,tenant_id,moysklad_account,ozon_account,actor,criterion,moy_import,ozon_import,apply_state,apply_total,confirmed,skipped,apply_code,created_at<clock_timestamp()-interval '1 hour' FROM product_matching_jobs WHERE id=$1 FOR UPDATE`, id)
	batch.Queue(`SAVEPOINT matching_apply`)
	results := tx.SendBatch(ctx, batch)
	err := results.QueryRow().Scan(&w.ID, &w.Scope.Tenant, &w.Scope.MoyskladAccount, &w.Scope.OzonAccount, &w.Actor, &w.Criterion, &w.MoyImport, &w.OzonImport, &app.State, &app.Total, &app.Confirmed, &app.Skipped, &app.Code, &w.Expired)
	if err != nil {
		return w, app, errors.Join(err, results.Close())
	}
	if _, err = results.Exec(); err != nil {
		return w, app, errors.Join(err, results.Close())
	}
	err = results.Close()
	return w, app, err
}
func applyProductMatchingSet(ctx context.Context, tx pgx.Tx, w productMatchingWork) (ProductMatchingApplication, error) {
	var app ProductMatchingApplication
	imports, err := productMatchingImports(ctx, tx, w.Scope)
	if err != nil {
		return app, err
	}
	if imports.MoyID != w.MoyImport || imports.OzonID != w.OzonImport {
		return app, catalogsync.ErrStale
	}
	if w.Expired {
		return app, catalogsync.ErrStale
	}
	explanation := fmt.Sprintf("Exact catalogue matching; criterion %s; job %s", w.Criterion, w.ID)
	err = tx.QueryRow(ctx, productMatchingApplySQL, w.ID, w.Criterion.Fields(), auditPrincipal(w.Actor), mappingSnapshotHash(reviewSnapshot{}), explanation).Scan(&app.Total, &app.Confirmed, &app.Skipped)
	if err != nil {
		return app, fmt.Errorf("apply exact matching set: %w", err)
	}
	app.State = "COMPLETED"
	return app, nil
}

func authorizeAndApplyMatching(ctx context.Context, tx pgx.Tx, w productMatchingWork) (ProductMatchingApplication, error) {
	if err := lockProductMatch(ctx, tx, w.Scope, w.Actor); err != nil {
		return ProductMatchingApplication{}, err
	}
	return applyProductMatchingSet(ctx, tx, w)
}
