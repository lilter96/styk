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
	Pages     int                        `json:"pages"`
	Code      string                     `json:"code"`
	Summary   map[string]int             `json:"summary"`
	Apply     ProductMatchingApplication `json:"apply"`
	Examples  []matching.Item            `json:"examples"`
	Attention []matching.Item            `json:"attention"`
	Preview   *ProductMatchPreview       `json:"preview,omitempty"`
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
	data, err := productMatchDataPage(ctx, tx, scope, "", criterion)
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
	return j, nil
}

func replayProductMatchingJob(ctx context.Context, tx pgx.Tx, scope catalog.Scope, actor, key string, criterion matching.Criterion) (ProductMatchingJob, bool, error) {
	var j ProductMatchingJob
	var previousCriterion matching.Criterion
	err := tx.QueryRow(ctx, `SELECT id,state,processed,total,pages,code,criterion,summary FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND actor=$4 AND request_key=$5`, append(catalogScopeArgs(scope), actor, key)...).Scan(&j.ID, &j.State, &j.Processed, &j.Total, &j.Pages, &j.Code, &previousCriterion, &j.Summary)
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
	return j, true, nil
}

func (s *Store) ProductMatchingJob(ctx context.Context, scope catalog.Scope, id, actor string, page int) (ProductMatchingJob, error) {
	var j ProductMatchingJob
	if scope.Validate() != nil || page < -1 {
		return j, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if _, err := readCatalogConfig(ctx, s.Pool, scope); err != nil {
		return j, err
	}
	args := append(catalogScopeArgs(scope), id, actor)
	err := s.Pool.QueryRow(ctx, `SELECT id,state,processed,total,pages,code,summary,criterion,apply_state,apply_total,confirmed,skipped,apply_code FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, args...).Scan(&j.ID, &j.State, &j.Processed, &j.Total, &j.Pages, &j.Code, &j.Summary, &j.Criterion, &j.Apply.State, &j.Apply.Total, &j.Apply.Confirmed, &j.Apply.Skipped, &j.Apply.Code)
	if err != nil {
		return j, catalogError("read matching job", err)
	}
	if j.State != "COMPLETED" {
		return j, nil
	}
	if err = s.readProductMatchingExamples(ctx, &j); err != nil {
		return j, err
	}
	if page < 0 {
		return j, nil
	}
	return s.readProductMatchingJobPage(ctx, j, page)
}

func (s *Store) readProductMatchingJobPage(ctx context.Context, j ProductMatchingJob, page int) (ProductMatchingJob, error) {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT p.preview FROM product_matching_job_pages jp JOIN product_matching_previews p ON p.id=jp.preview_id WHERE jp.job_id=$1 AND jp.page=$2`, j.ID, page).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, nil
	}
	if err != nil {
		return j, err
	}
	j.Preview = &ProductMatchPreview{}
	return j, catalogDecode(raw, j.Preview)
}

func (s *Store) RunProductMatchingJob(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackMerchantMutation(tx)
	w, err := claimProductMatchingWork(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT matching_page`); err != nil {
		return false, err
	}
	applying := w.ApplyState == "QUEUED" || w.ApplyState == "RUNNING"
	if applying {
		err = processProductMatchingApplication(ctx, tx, w)
	} else {
		err = processProductMatchingPage(ctx, tx, w.ID, w.Scope, w.Actor, w.Criterion, w.MoyImport, w.OzonImport, w.After, w.Pages)
	}
	if err = finishProductMatchingPage(ctx, tx, w.ID, err, applying); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

type productMatchingWork struct {
	ID, Actor, After, ApplyState  string
	Scope                         catalog.Scope
	Criterion                     matching.Criterion
	MoyImport, OzonImport         int64
	Pages, ApplyPage, ApplyOffset int
}

func claimProductMatchingWork(ctx context.Context, tx pgx.Tx) (productMatchingWork, error) {
	var w productMatchingWork
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,moysklad_account,ozon_account,actor,criterion,moy_import,ozon_import,cursor,pages,apply_state,apply_page,apply_offset FROM product_matching_jobs WHERE state IN ('QUEUED','RUNNING') OR apply_state IN ('QUEUED','RUNNING') ORDER BY updated_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&w.ID, &w.Scope.Tenant, &w.Scope.MoyskladAccount, &w.Scope.OzonAccount, &w.Actor, &w.Criterion, &w.MoyImport, &w.OzonImport, &w.After, &w.Pages, &w.ApplyState, &w.ApplyPage, &w.ApplyOffset)
	return w, err
}

func finishProductMatchingPage(ctx context.Context, tx pgx.Tx, id string, pageErr error, applying bool) error {
	err := pageErr
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT matching_page`); rollbackErr != nil {
			return rollbackErr
		}
		code, state := "MATCHING_SERVICE_UNAVAILABLE", "FAILED"
		if errors.Is(err, catalogsync.ErrStale) {
			code, state = "MATCHING_SOURCE_STALE", "STALE"
		}
		if errors.Is(err, catalogsync.ErrBlocked) {
			code, state = "MATCHING_AUTHORIZATION_CHANGED", "STALE"
		}
		query := `UPDATE product_matching_jobs SET state=$2,code=$3,updated_at=clock_timestamp() WHERE id=$1`
		if applying {
			query = `UPDATE product_matching_jobs SET apply_state=$2,apply_code=$3,updated_at=clock_timestamp() WHERE id=$1`
		}
		if _, err = tx.Exec(ctx, query, id, state, code); err != nil {
			return err
		}
	}
	return nil
}

func processProductMatchingPage(ctx context.Context, tx pgx.Tx, id string, scope catalog.Scope, actor string, criterion matching.Criterion, mi, oi int64, after string, page int) error {
	if err := lockProductMatch(ctx, tx, scope, actor); err != nil {
		return err
	}
	policy := matching.Policy{ExactSKU: true, AutoConfirm: true, Criterion: criterion}
	data, err := productMatchDataPage(ctx, tx, scope, after, criterion)
	if err != nil {
		return err
	}
	if data.MoyID != mi || data.OzonID != oi {
		return catalogsync.ErrStale
	}
	p, err := persistProductMatchPreview(ctx, tx, scope, policy, data, actor)
	if err != nil {
		return err
	}
	return saveProductMatchingPage(ctx, tx, id, after, page, p, data)
}

func saveProductMatchingPage(ctx context.Context, tx pgx.Tx, id, after string, page int, p ProductMatchPreview, data productMatchSnapshot) error {
	if _, err := tx.Exec(ctx, `INSERT INTO product_matching_job_pages(job_id,page,preview_id) VALUES($1,$2,$3)`, id, page, p.ID); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, item := range p.Items {
		counts[item.Status]++
	}
	stats, err := catalogJSON(counts)
	if err != nil {
		return err
	}
	state := "COMPLETED"
	if data.HasNext {
		state = "RUNNING"
	}
	cursor := after
	if len(data.Sources) > 0 {
		cursor = data.Sources[len(data.Sources)-1].Match.Identity.ID
	}
	_, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET state=$2,cursor=$3,processed=processed+$4,pages=pages+1,summary=summary||COALESCE((SELECT jsonb_object_agg(key,COALESCE((summary->>key)::int,0)+value::int) FROM jsonb_each_text($5::jsonb)),'{}'::jsonb),updated_at=clock_timestamp() WHERE id=$1`, id, state, cursor, len(data.Sources), stats)
	return err
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
	tag, err := tx.Exec(ctx, `UPDATE product_matching_jobs SET state='STALE',code='CANCELLED',apply_state=CASE WHEN apply_state IN ('QUEUED','RUNNING') THEN 'CANCELLED' ELSE apply_state END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, append(catalogScopeArgs(scope), id, actor)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return catalogsync.ErrNotFound
	}
	return tx.Commit(ctx)
}
