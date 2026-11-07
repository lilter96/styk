package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/matching"
)

type ProductMatchingApproval struct {
	Scope             catalog.Scope
	JobID, Actor, Key string
	Expected          int
	Acknowledge       bool
}

func (s *Store) ApplyProductMatchingJob(ctx context.Context, r ProductMatchingApproval) error {
	if r.Scope.Validate() != nil || !r.Acknowledge || r.Expected <= 0 || !importKeyPattern.MatchString(r.Key) {
		return catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackMerchantMutation(tx)
	if err = queueProductMatchingApplication(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func queueProductMatchingApplication(ctx context.Context, tx pgx.Tx, r ProductMatchingApproval) error {
	var state, apply, key string
	var count, total int
	var mi, oi int64
	var criterion matching.Criterion
	err := tx.QueryRow(ctx, `SELECT state,apply_state,apply_key,COALESCE((summary->>'exact')::int,0),apply_total,moy_import,ozon_import,criterion FROM product_matching_jobs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5 FOR UPDATE`, append(catalogScopeArgs(r.Scope), r.JobID, r.Actor)...).Scan(&state, &apply, &key, &count, &total, &mi, &oi, &criterion)
	if err != nil {
		return catalogError("read matching approval", err)
	}
	if err = lockProductMatch(ctx, tx, r.Scope, r.Actor); err != nil {
		return err
	}
	if apply != "NOT_STARTED" {
		if key != r.Key || total != r.Expected {
			return catalogsync.ErrConflict
		}
		return nil
	}
	if state != "COMPLETED" || count != r.Expected {
		return catalogsync.ErrStale
	}
	if err = validateProductMatchingRetry(ctx, tx, r.Scope, 0, mi, oi, "", criterion); err != nil {
		return err
	}
	var expired bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_matching_job_pages jp JOIN product_matching_previews p ON p.id=jp.preview_id WHERE jp.job_id=$1 AND (p.preview->>'expires_at')::timestamptz<=clock_timestamp())`, r.JobID).Scan(&expired)
	if err != nil {
		return err
	}
	if expired {
		return catalogsync.ErrStale
	}
	_, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state='QUEUED',apply_key=$2,apply_total=$3,updated_at=clock_timestamp() WHERE id=$1`, r.JobID, r.Key, r.Expected)
	return err
}

func processProductMatchingApplication(ctx context.Context, tx pgx.Tx, w productMatchingWork) error {
	if err := lockProductMatch(ctx, tx, w.Scope, w.Actor); err != nil {
		return err
	}
	p, err := readProductMatchingApplicationPage(ctx, tx, w)
	if err != nil {
		return err
	}
	data, err := productMatchDataPage(ctx, tx, w.Scope, p.After, w.Criterion)
	if err != nil {
		return err
	}
	if data.MoyID != w.MoyImport || data.OzonID != w.OzonImport {
		return catalogsync.ErrStale
	}
	fresh := matching.Preview(w.Scope.Tenant, w.Scope.MoyskladAccount, p.Policy, data.Sources, data.Targets, data.Mappings)
	checked, next := productMatchingApplicationBatch(p, fresh, w.ApplyOffset)
	result, err := saveProductMatchingApplicationBatch(ctx, tx, w, checked)
	if err != nil {
		return err
	}
	page := w.ApplyPage
	if next >= len(p.Items) {
		page++
		next = 0
	}
	state := "RUNNING"
	if page >= w.Pages {
		state = "COMPLETED"
	}
	_, err = tx.Exec(ctx, `UPDATE product_matching_jobs SET apply_state=$2,apply_page=$3,apply_offset=$4,confirmed=confirmed+$5,skipped=skipped+$6,updated_at=clock_timestamp() WHERE id=$1`, w.ID, state, page, next, result.Confirmed, len(checked.Items)-result.Confirmed)
	return err
}

func readProductMatchingApplicationPage(ctx context.Context, tx pgx.Tx, w productMatchingWork) (ProductMatchPreview, error) {
	var p ProductMatchPreview
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT p.preview FROM product_matching_job_pages jp JOIN product_matching_previews p ON p.id=jp.preview_id WHERE jp.job_id=$1 AND jp.page=$2`, w.ID, w.ApplyPage).Scan(&raw)
	if err != nil {
		return p, err
	}
	if err = catalogDecode(raw, &p); err != nil {
		return p, err
	}
	if p.Scope != w.Scope || p.Policy.Criterion.Resolved() != w.Criterion || !p.ExpiresAt.After(time.Now()) {
		return p, catalogsync.ErrStale
	}
	return p, nil
}

func productMatchingApplicationBatch(p ProductMatchPreview, fresh []matching.Item, offset int) (ProductMatchPreview, int) {
	current := make(map[string]matching.Item, len(fresh))
	for _, item := range fresh {
		current[item.ProductID] = item
	}
	end := min(offset+20, len(p.Items))
	checked := p
	checked.Items = make([]matching.Item, 0, 20)
	for _, item := range p.Items[offset:end] {
		if item.Status != "exact" || len(item.Candidates) != 1 {
			continue
		}
		candidate, found := current[item.ProductID]
		same := found && candidate.Status == "exact" && len(candidate.Candidates) == 1 && candidate.Candidates[0].ID == item.Candidates[0].ID && candidate.Candidates[0].Kind == item.Candidates[0].Kind
		if !same {
			item.Status, item.Reason = "skipped", "matching_changed"
		}
		checked.Items = append(checked.Items, item)
	}
	return checked, end
}

func saveProductMatchingApplicationBatch(ctx context.Context, tx pgx.Tx, w productMatchingWork, p ProductMatchPreview) (ProductMatchRun, error) {
	if len(p.Items) == 0 {
		return ProductMatchRun{}, nil
	}
	key, err := catalogHash([]any{w.ID, w.ApplyPage, w.ApplyOffset})
	if err != nil {
		return ProductMatchRun{}, err
	}
	ids := make([]string, 0, len(p.Items))
	for _, item := range p.Items {
		ids = append(ids, item.ProductID)
	}
	req := ProductMatchExecute{Scope: w.Scope, Actor: w.Actor, PreviewID: p.ID, Products: ids, IdempotencyKey: key, Acknowledge: true}
	result, err := confirmProductMatchItems(ctx, tx, req, p)
	if err != nil {
		return result, err
	}
	hash, err := catalogHash(req)
	if err != nil {
		return result, err
	}
	return result, saveProductMatchRun(ctx, tx, req, result, hash)
}
