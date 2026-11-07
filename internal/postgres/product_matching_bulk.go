package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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
	var state, apply, key string
	var count, total int
	err = tx.QueryRow(ctx, `SELECT state,apply_state,apply_key,COALESCE((summary->>'exact')::int,0),apply_total FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5 FOR UPDATE`, append(catalogScopeArgs(r.Scope), r.JobID, r.Actor)...).Scan(&state, &apply, &key, &count, &total)
	if err != nil {
		return catalogError("read matching approval", err)
	}
	if err = catalogActor(ctx, tx, r.Scope.Tenant, r.Actor); err != nil {
		return err
	}
	if apply != "NOT_STARTED" {
		if key != r.Key || total != r.Expected {
			return catalogsync.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if state != "COMPLETED" || count != r.Expected {
		return catalogsync.ErrStale
	}
	_, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state='QUEUED',apply_key=$2,apply_total=$3,updated_at=clock_timestamp() WHERE id=$1`, r.JobID, r.Key, r.Expected)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
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
		if ctx.Err() != nil {
			return app, err
		}
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT matching_apply`); rollbackErr != nil {
			return app, errors.Join(err, rollbackErr)
		}
		app.State, app.Code = "FAILED", "MATCHING_SERVICE_UNAVAILABLE"
		if errors.Is(err, catalogsync.ErrStale) || errors.Is(err, catalogsync.ErrBlocked) {
			app.State, app.Code = "STALE", "MATCHING_SOURCE_STALE"
		}
		if _, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state=$2,apply_code=$3,updated_at=clock_timestamp() WHERE id=$1`, id, app.State, app.Code); err != nil {
			return app, err
		}
	} else {
		app = result
	}
	return app, tx.Commit(ctx)
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
