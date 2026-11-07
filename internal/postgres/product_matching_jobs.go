package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/matching"
)

type ProductMatchingApplication struct {
	State     string `json:"state"`
	Total     int    `json:"total"`
	Confirmed int    `json:"confirmed"`
	Skipped   int    `json:"skipped"`
	Code      string `json:"code"`
}
type ProductMatchingJob struct {
	ID        string                     `json:"id"`
	State     string                     `json:"state"`
	Criterion matching.Criterion         `json:"criterion"`
	Processed int                        `json:"processed"`
	Total     int                        `json:"total"`
	Code      string                     `json:"code"`
	Summary   map[string]int             `json:"summary"`
	Apply     ProductMatchingApplication `json:"apply"`
	Examples  []matching.Item            `json:"examples"`
	Attention []matching.Item            `json:"attention"`
}

func (s *Store) CreateProductMatchingJob(ctx context.Context, scope catalog.Scope, actor, key string, criterion matching.Criterion) (ProductMatchingJob, error) {
	var j ProductMatchingJob
	if scope.Validate() != nil || !criterion.Valid() || !importKeyPattern.MatchString(key) {
		return j, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return j, err
	}
	defer rollbackMerchantMutation(tx)
	if err = lockProductMatch(ctx, tx, scope, actor); err != nil {
		return j, err
	}
	j, err = createProductMatchingJob(ctx, tx, scope, actor, key, criterion)
	if err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}

func createProductMatchingJob(ctx context.Context, tx pgx.Tx, scope catalog.Scope, actor, key string, criterion matching.Criterion) (ProductMatchingJob, error) {
	j, found, err := replayProductMatchingJob(ctx, tx, scope, actor, key, criterion)
	if err != nil || found {
		return j, err
	}
	c, err := readCatalogConfig(ctx, tx, scope)
	if err != nil {
		return j, err
	}
	if _, err = initializeImportConfig(ctx, tx, c, actor); err != nil {
		return j, err
	}
	p, err := readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return j, err
	}
	data, err := productMatchingImports(ctx, tx, scope)
	if err != nil {
		return j, err
	}
	j.ID, err = catalogID()
	if err != nil {
		return j, err
	}
	j.State, j.Total, j.Criterion = "QUEUED", data.Total, criterion
	j.Apply.State = "NOT_STARTED"
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_jobs(id,tenant_id,moysklad_account,ozon_account,actor,request_key,revision,moy_import,ozon_import,state,total,criterion) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'QUEUED',$10,$11)`, j.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, actor, key, p.Revision, data.MoyID, data.OzonID, j.Total, string(criterion))
	if err != nil {
		return j, fmt.Errorf("queue matching job: %w", err)
	}
	return completeProductMatchingSearch(ctx, tx, j, scope, actor, criterion, data)
}

func replayProductMatchingJob(ctx context.Context, tx pgx.Tx, scope catalog.Scope, actor, key string, criterion matching.Criterion) (ProductMatchingJob, bool, error) {
	var j ProductMatchingJob
	var previousCriterion matching.Criterion
	err := tx.QueryRow(ctx, `SELECT id,state,processed,total,code,criterion,summary,apply_state,apply_total,confirmed,skipped,apply_code FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND actor=$4 AND request_key=$5`, append(catalogScopeArgs(scope), actor, key)...).Scan(&j.ID, &j.State, &j.Processed, &j.Total, &j.Code, &previousCriterion, &j.Summary, &j.Apply.State, &j.Apply.Total, &j.Apply.Confirmed, &j.Apply.Skipped, &j.Apply.Code)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	if previousCriterion != criterion {
		return j, false, catalogsync.ErrConflict
	}
	j.Criterion = previousCriterion
	err = readProductMatchingExamples(ctx, tx, &j)
	return j, true, err
}

func (s *Store) ProductMatchingJob(ctx context.Context, scope catalog.Scope, id, actor string) (ProductMatchingJob, error) {
	var j ProductMatchingJob
	if scope.Validate() != nil {
		return j, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if _, err := readCatalogConfig(ctx, s.Pool, scope); err != nil {
		return j, err
	}
	err := s.Pool.QueryRow(ctx, `SELECT id,state,processed,total,code,summary,criterion,apply_state,apply_total,confirmed,skipped,apply_code FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, append(catalogScopeArgs(scope), id, actor)...).Scan(&j.ID, &j.State, &j.Processed, &j.Total, &j.Code, &j.Summary, &j.Criterion, &j.Apply.State, &j.Apply.Total, &j.Apply.Confirmed, &j.Apply.Skipped, &j.Apply.Code)
	if err != nil {
		return j, catalogError("read matching job", err)
	}
	if err = readProductMatchingExamples(ctx, s.Pool, &j); err != nil {
		return j, err
	}
	return j, nil
}

type productMatchingWork struct {
	ID, Actor             string
	Scope                 catalog.Scope
	Criterion             matching.Criterion
	MoyImport, OzonImport int64
}

func (s *Store) RunProductMatchingJob(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM product_matching_jobs WHERE apply_state IN ('QUEUED','RUNNING') ORDER BY updated_at,id LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = s.executeProductMatchingApplication(ctx, id)
	return true, err
}
func (s *Store) CancelProductMatchingJob(ctx context.Context, scope catalog.Scope, id, actor string) error {
	if scope.Validate() != nil {
		return catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, scope.Tenant, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state=CASE WHEN apply_state IN ('QUEUED','RUNNING') THEN 'CANCELLED' ELSE apply_state END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, append(catalogScopeArgs(scope), id, actor)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return catalogsync.ErrNotFound
	}
	return tx.Commit(ctx)
}
