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

type ProductMatchingJob struct {
	ID        string               `json:"id"`
	State     string               `json:"state"`
	Processed int                  `json:"processed"`
	Total     int                  `json:"total"`
	Pages     int                  `json:"pages"`
	Code      string               `json:"code"`
	Summary   map[string]int       `json:"summary"`
	Preview   *ProductMatchPreview `json:"preview,omitempty"`
}

func (s *Store) CreateProductMatchingJob(ctx context.Context, scope catalog.Scope, revision int64, actor, key string) (ProductMatchingJob, error) {
	var j ProductMatchingJob
	if scope.Validate() != nil || !importKeyPattern.MatchString(key) {
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
	// Replay before checking changed imports or policy; the same intent owns one job.
	args := append(catalogScopeArgs(scope), actor, key)
	var previousRevision int64
	err = tx.QueryRow(ctx, `SELECT id,state,processed,total,pages,code,revision FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND actor=$4 AND request_key=$5`, args...).Scan(&j.ID, &j.State, &j.Processed, &j.Total, &j.Pages, &j.Code, &previousRevision)
	if err == nil {
		if previousRevision != revision {
			return j, catalogsync.ErrConflict
		}
		return j, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
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
	if p.Revision != revision {
		return j, catalogsync.ErrStale
	}
	data, err := productMatchData(ctx, tx, scope)
	if err != nil {
		return j, err
	}
	j.ID, err = catalogID()
	if err != nil {
		return j, err
	}
	j.State = "QUEUED"
	j.Total = data.Total
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_jobs(id,tenant_id,moysklad_account,ozon_account,actor,request_key,revision,moy_import,ozon_import,state,total) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'QUEUED',$10)`, j.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, actor, key, revision, data.MoyID, data.OzonID, j.Total)
	if err != nil {
		return j, fmt.Errorf("queue matching job: %w", err)
	}
	return j, tx.Commit(ctx)
}
func (s *Store) ProductMatchingJob(ctx context.Context, scope catalog.Scope, id, actor string, page int) (ProductMatchingJob, error) {
	var j ProductMatchingJob
	if scope.Validate() != nil || page < 0 {
		return j, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if _, err := readCatalogConfig(ctx, s.Pool, scope); err != nil {
		return j, err
	}
	args := append(catalogScopeArgs(scope), id, actor)
	err := s.Pool.QueryRow(ctx, `SELECT id,state,processed,total,pages,code,summary FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, args...).Scan(&j.ID, &j.State, &j.Processed, &j.Total, &j.Pages, &j.Code, &j.Summary)
	if err != nil {
		return j, catalogError("read matching job", err)
	}
	if j.State != "COMPLETED" {
		return j, nil
	}
	var raw []byte
	err = s.Pool.QueryRow(ctx, `SELECT p.preview FROM product_matching_job_pages jp JOIN product_matching_previews p ON p.id=jp.preview_id WHERE jp.job_id=$1 AND jp.page=$2`, id, page).Scan(&raw)
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
	var id, actor, after string
	var scope catalog.Scope
	var revision, mi, oi int64
	var pages int
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,moysklad_account,ozon_account,actor,revision,moy_import,ozon_import,cursor,pages FROM product_matching_jobs WHERE state IN ('QUEUED','RUNNING') ORDER BY updated_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount, &actor, &revision, &mi, &oi, &after, &pages)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT matching_page`); err != nil {
		return false, err
	}
	err = processProductMatchingPage(ctx, tx, id, scope, actor, revision, mi, oi, after, pages)
	if err != nil {
		if ctx.Err() != nil {
			return false, err
		}
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT matching_page`); rollbackErr != nil {
			return false, rollbackErr
		}
		code, state := "MATCHING_SERVICE_UNAVAILABLE", "FAILED"
		if errors.Is(err, catalogsync.ErrStale) {
			code, state = "MATCHING_SOURCE_STALE", "STALE"
		}
		if errors.Is(err, catalogsync.ErrBlocked) {
			code, state = "MATCHING_AUTHORIZATION_CHANGED", "STALE"
		}
		if _, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET state=$2,code=$3,updated_at=clock_timestamp() WHERE id=$1`, id, state, code); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
func processProductMatchingPage(ctx context.Context, tx pgx.Tx, id string, scope catalog.Scope, actor string, revision, mi, oi int64, after string, page int) error {
	if err := lockProductMatch(ctx, tx, scope, actor); err != nil {
		return err
	}
	policy, err := readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return err
	}
	if policy.Revision != revision {
		return catalogsync.ErrStale
	}
	data, err := productMatchDataPage(ctx, tx, scope, after)
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
	if _, err = tx.Exec(ctx, `INSERT INTO product_matching_job_pages(job_id,page,preview_id) VALUES($1,$2,$3)`, id, page, p.ID); err != nil {
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
	tag, err := tx.Exec(ctx, `UPDATE product_matching_jobs SET state='STALE',code='CANCELLED',updated_at=clock_timestamp() WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, append(catalogScopeArgs(scope), id, actor)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return catalogsync.ErrNotFound
	}
	return tx.Commit(ctx)
}
