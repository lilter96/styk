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
	"github.com/lilter96/styk/internal/matching"
)

type ProductMatchPreview struct {
	ID          string          `json:"id"`
	Scope       catalog.Scope   `json:"scope"`
	Policy      matching.Policy `json:"policy"`
	Fingerprint string          `json:"fingerprint"`
	Items       []matching.Item `json:"items"`
	ExpiresAt   time.Time       `json:"expires_at"`
}
type ProductMatchRun struct {
	ID        string          `json:"id"`
	PreviewID string          `json:"preview_id"`
	Items     []matching.Item `json:"items"`
	Confirmed int             `json:"confirmed"`
}
type ProductMatchExecute struct {
	Scope                            catalog.Scope
	PreviewID, Actor, IdempotencyKey string
	Products                         []string
	Acknowledge                      bool
}

func readProductMatchPolicy(ctx context.Context, q catalogQuerier, scope catalog.Scope) (matching.Policy, error) {
	if _, err := readCatalogConfig(ctx, q, scope); err != nil {
		return matching.Policy{}, err
	}
	var raw []byte
	p := matching.Policy{ExactSKU: true, Names: true}
	err := q.QueryRow(ctx, `SELECT policy FROM product_matching_policies WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, catalogScopeArgs(scope)...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("load matching policy: %w", err)
	}
	return p, catalogDecode(raw, &p)
}
func (s *Store) ProductMatchPolicy(ctx context.Context, scope catalog.Scope) (matching.Policy, error) {
	if scope.Validate() != nil {
		return matching.Policy{}, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readProductMatchPolicy(ctx, s.Pool, scope)
}
func (s *Store) SaveProductMatchPolicy(ctx context.Context, scope catalog.Scope, p matching.Policy, actor string) (matching.Policy, error) {
	if scope.Validate() != nil || p.Revision < 0 || p.AutoConfirm && !p.ExactSKU || p.AfterImport && !p.AutoConfirm {
		return p, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer rollbackMerchantMutation(tx)
	if err = lockProductMatch(ctx, tx, scope, actor); err != nil {
		return p, err
	}
	previous, err := readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return p, err
	}
	if p.Revision != previous.Revision {
		return p, catalogsync.ErrStale
	}
	p.Revision++
	if err = saveProductMatchPolicy(ctx, tx, scope, p, actor); err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}
func lockProductMatch(ctx context.Context, tx pgx.Tx, scope catalog.Scope, actor string) error {
	if err := catalogActor(ctx, tx, scope.Tenant, actor); err != nil {
		return err
	}
	return lockReview(ctx, tx, MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount})
}

func (s *Store) PreviewProductMatches(ctx context.Context, scope catalog.Scope, revision int64, actor string) (ProductMatchPreview, error) {
	var p ProductMatchPreview
	if scope.Validate() != nil {
		return p, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer rollbackMerchantMutation(tx)
	if err = lockProductMatch(ctx, tx, scope, actor); err != nil {
		return p, err
	}
	p.Scope = scope
	p.Policy, err = readProductMatchPolicy(ctx, tx, scope)
	if err != nil {
		return p, err
	}
	if p.Policy.Revision != revision {
		return p, catalogsync.ErrStale
	}
	data, err := productMatchData(ctx, tx, scope)
	if err != nil {
		return p, err
	}
	p, err = persistProductMatchPreview(ctx, tx, scope, p.Policy, data, actor)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func (s *Store) ExecuteProductMatches(ctx context.Context, req ProductMatchExecute) (ProductMatchRun, error) {
	var result ProductMatchRun
	if req.Scope.Validate() != nil || !req.Acknowledge || len(req.Products) == 0 || len(req.Products) > 100 || !importKeyPattern.MatchString(req.IdempotencyKey) {
		return result, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer rollbackMerchantMutation(tx)
	if err = lockProductMatch(ctx, tx, req.Scope, req.Actor); err != nil {
		return result, err
	}
	hash, err := catalogHash(req)
	if err != nil {
		return result, err
	}
	result, err = replayProductMatchRun(ctx, tx, req, hash)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, catalogsync.ErrNotFound) {
		return result, err
	}
	p, err := currentProductMatchPreview(ctx, tx, req)
	if err != nil {
		return result, err
	}
	result, err = confirmProductMatchItems(ctx, tx, req, p)
	if err != nil {
		return result, err
	}
	if err = saveProductMatchRun(ctx, tx, req, result, hash); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func replayProductMatchRun(ctx context.Context, tx pgx.Tx, req ProductMatchExecute, hash string) (ProductMatchRun, error) {
	var raw []byte
	var previous string
	var result ProductMatchRun
	args := append(catalogScopeArgs(req.Scope), req.IdempotencyKey, req.Actor)
	err := tx.QueryRow(ctx, `SELECT result,request_hash FROM product_matching_runs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND idempotency_key=$4 AND actor=$5`, args...).Scan(&raw, &previous)
	if err != nil {
		return result, catalogError("read matching run", err)
	}
	if previous != hash {
		return result, catalogsync.ErrConflict
	}
	return result, catalogDecode(raw, &result)
}
func currentProductMatchPreview(ctx context.Context, tx pgx.Tx, req ProductMatchExecute) (ProductMatchPreview, error) {
	var raw []byte
	var p ProductMatchPreview
	args := append(catalogScopeArgs(req.Scope), req.PreviewID, req.Actor)
	err := tx.QueryRow(ctx, `SELECT preview FROM product_matching_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND actor=$5`, args...).Scan(&raw)
	if err != nil {
		return p, catalogError("read matching preview", err)
	}
	if err = catalogDecode(raw, &p); err != nil {
		return p, err
	}
	policy, err := readProductMatchPolicy(ctx, tx, req.Scope)
	if err != nil {
		return p, err
	}
	if !policy.AutoConfirm {
		return p, catalogsync.ErrBlocked
	}
	if p.Policy != policy || !p.ExpiresAt.After(time.Now()) {
		return p, catalogsync.ErrStale
	}
	data, err := productMatchData(ctx, tx, req.Scope)
	if err != nil {
		return p, err
	}
	hash, err := catalogHash(data)
	if err != nil {
		return p, err
	}
	if hash != p.Fingerprint {
		return p, catalogsync.ErrStale
	}
	return p, nil
}
func confirmProductMatchItems(ctx context.Context, tx pgx.Tx, req ProductMatchExecute, p ProductMatchPreview) (ProductMatchRun, error) {
	id, err := catalogID()
	if err != nil {
		return ProductMatchRun{}, err
	}
	result := ProductMatchRun{ID: id, PreviewID: p.ID, Items: make([]matching.Item, 0, len(req.Products))}
	selected := make(map[string]bool, len(req.Products))
	for _, id := range req.Products {
		if selected[id] {
			return result, catalogsync.ErrInvalid
		}
		selected[id] = true
	}
	for _, item := range p.Items {
		if !selected[item.ProductID] {
			continue
		}
		delete(selected, item.ProductID)
		if item.Status == "exact" && len(item.Candidates) == 1 {
			if err := confirmProductMatchItem(ctx, tx, req, item); err != nil {
				return result, err
			}
			item.Status, item.Reason = "confirmed", "confirmed_by_rule"
			result.Confirmed++
		}
		result.Items = append(result.Items, item)
	}
	if len(selected) > 0 {
		return result, catalogsync.ErrInvalid
	}
	return result, nil
}
func confirmProductMatchItem(ctx context.Context, tx pgx.Tx, req ProductMatchExecute, item matching.Item) error {
	candidate := item.Candidates[0]
	review, err := createMappingReviewTx(ctx, tx, domain.MappingReviewRequest{Tenant: req.Scope.Tenant, MoyskladAccount: req.Scope.MoyskladAccount, OzonAccount: req.Scope.OzonAccount, ProductID: item.ProductID, TargetKind: candidate.Kind, TargetID: candidate.ID, Evidence: domain.MappingDeterministicCandidate, SessionID: req.Actor, Lifetime: 5 * time.Minute})
	if err != nil {
		return err
	}
	confirmation := domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: req.Actor, IdempotencyKey: req.IdempotencyKey, Acknowledged: true, Explanation: "Unique exact match; policy opt-in; preview " + req.PreviewID}
	fingerprint, err := confirmationFingerprint(confirmation)
	if err != nil {
		return err
	}
	_, err = confirmMappingReviewTx(ctx, tx, confirmation, fingerprint)
	return err
}
func saveProductMatchRun(ctx context.Context, tx pgx.Tx, req ProductMatchExecute, result ProductMatchRun, hash string) error {
	raw, err := catalogJSON(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_matching_runs(id,preview_id,tenant_id,moysklad_account,ozon_account,actor,idempotency_key,request_hash,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, result.ID, req.PreviewID, req.Scope.Tenant, req.Scope.MoyskladAccount, req.Scope.OzonAccount, req.Actor, req.IdempotencyKey, hash, raw)
	if err != nil {
		return catalogError("save matching result", err)
	}
	return nil
}
