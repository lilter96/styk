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

// Readback proves the created identity. Full imports subsequently make that
// identity eligible for the same review/history transaction used by manual links.
func (s *Store) RunCreatedProductLink(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT w.work FROM catalog_sync_work w LEFT JOIN catalog_created_links l ON l.work_id=w.id WHERE w.state='RECONCILED' AND w.work#>>'{command,action}'='create' AND w.work->'created_target' IS NOT NULL AND (l.work_id IS NULL OR (l.state='WAITING' AND l.checked_at<clock_timestamp()-interval '30 seconds')) ORDER BY COALESCE(l.checked_at,w.updated_at),w.id LIMIT 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find verified creation link: %w", err)
	}
	var work catalogsync.Work
	if err = catalogDecode(raw, &work); err != nil {
		return false, err
	}
	state, err := s.linkCreatedProduct(ctx, work)
	if err != nil {
		return false, err
	}
	if state == "WAITING" {
		if err = s.refreshCreatedProductCatalogs(ctx, work); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Store) linkCreatedProduct(ctx context.Context, w catalogsync.Work) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin created product link: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogMappingLocks(ctx, tx, w.Scope); err != nil {
		return "", err
	}
	request, err := createdProductReview(w)
	if err != nil {
		return "", err
	}
	state, id, err := confirmCreatedProduct(ctx, tx, w, request)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO catalog_created_links(work_id,state,mapping_id,checked_at) VALUES($1,$2,$3,clock_timestamp()) ON CONFLICT(work_id) DO UPDATE SET state=EXCLUDED.state,mapping_id=EXCLUDED.mapping_id,checked_at=EXCLUDED.checked_at`, w.ID, state, id)
	if err != nil {
		return "", fmt.Errorf("record created product link: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit created product link: %w", err)
	}
	return state, nil
}

func createdProductReview(w catalogsync.Work) (domain.MappingReviewRequest, error) {
	if w.State != catalogsync.StateReconciled || w.CreatedTarget == nil || !w.Scope.Contains(w.CreatedTarget.Account) || !w.Scope.Contains(w.Command.Source.Account) || w.Command.Action != catalog.ActionCreate {
		return domain.MappingReviewRequest{}, catalogsync.ErrInvalid
	}
	source, target := w.Command.Source, *w.CreatedTarget
	if source.Account.Provider == catalog.ProviderMoySklad {
		source, target = target, source
	}
	if source.Account.Provider != catalog.ProviderOzon || target.Account.Provider != catalog.ProviderMoySklad || source.ID == "" || target.ID == "" {
		return domain.MappingReviewRequest{}, catalogsync.ErrInvalid
	}
	return domain.MappingReviewRequest{Tenant: w.Scope.Tenant, OzonAccount: w.Scope.OzonAccount, MoyskladAccount: w.Scope.MoyskladAccount, ProductID: source.ID, TargetKind: target.EntityType, TargetID: target.ID, SessionID: "catalog-created:" + w.ID, Evidence: domain.MappingOperatorAttested, Lifetime: 5 * time.Minute}, nil
}

func confirmCreatedProduct(ctx context.Context, tx pgx.Tx, w catalogsync.Work, req domain.MappingReviewRequest) (string, *int64, error) {
	snap, err := snapshotReview(ctx, tx, req)
	if errors.Is(err, ErrReviewSource) {
		return "WAITING", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	offer := w.Command.Source.OfferID
	if w.Command.Source.Account.Provider == catalog.ProviderMoySklad {
		offer = w.CreatedTarget.OfferID
	}
	if offer == "" || offer != snap.Offer {
		return "CONFLICT", nil, nil
	}
	if snap.Conflict {
		return "CONFLICT", nil, nil
	}
	if snap.MappingID != nil {
		return "LINKED", snap.MappingID, nil
	}
	current, err := createdProductImportsCurrent(ctx, tx, w, snap)
	if err != nil {
		return "", nil, err
	}
	if !current {
		return "WAITING", nil, nil
	}
	return recordCreatedProductMapping(ctx, tx, w, req)
}

func createdProductImportsCurrent(ctx context.Context, tx pgx.Tx, w catalogsync.Work, snap reviewSnapshot) (bool, error) {
	var current bool
	err := tx.QueryRow(ctx, `SELECT
 (SELECT id FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1)=$3
 AND (SELECT id FROM source_imports WHERE source='moysklad' AND account_id=$2 ORDER BY id DESC LIMIT 1)=$4
 AND (SELECT count(*)=2 FROM source_imports WHERE id IN ($3,$4) AND finished_at BETWEEN clock_timestamp()-interval '1 hour' AND clock_timestamp())
 AND EXISTS(SELECT 1 FROM source_entities WHERE import_id=$4 AND external_id=$5 AND kind=$6 AND archived=false)`, w.Scope.OzonAccount, w.Scope.MoyskladAccount, snap.OzonID, snap.MoyID, createdTargetID(w), createdTargetKind(w)).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("validate creation import evidence: %w", err)
	}
	return current && snap.Archived != nil && !*snap.Archived, nil
}
func createdTargetID(w catalogsync.Work) string {
	if w.Command.Source.Account.Provider == catalog.ProviderMoySklad {
		return w.Command.Source.ID
	}
	return w.CreatedTarget.ID
}
func createdTargetKind(w catalogsync.Work) string {
	if w.Command.Source.Account.Provider == catalog.ProviderMoySklad {
		return w.Command.Source.EntityType
	}
	return w.CreatedTarget.EntityType
}

func (s *Store) refreshCreatedProductCatalogs(ctx context.Context, w catalogsync.Work) error {
	for _, provider := range []string{"moysklad", "ozon"} {
		account, limits := w.Scope.OzonAccount, domain.DefaultOzonImportLimits()
		if provider == "moysklad" {
			account, limits = w.Scope.MoyskladAccount, domain.DefaultMoyskladImportLimits()
		}
		_, _, err := s.SubmitScheduledCatalogImport(ctx, w.Scope.Tenant, provider, account, "created-link:"+w.ID+":"+provider, "catalog-created:"+w.ID, limits)
		if err != nil && !errors.Is(err, ErrImportConflict) {
			return fmt.Errorf("refresh verified creation catalog: %w", err)
		}
	}
	return nil
}

func recordCreatedProductMapping(ctx context.Context, tx pgx.Tx, w catalogsync.Work, req domain.MappingReviewRequest) (string, *int64, error) {
	review, err := createMappingReviewTx(ctx, tx, req)
	if err != nil {
		return "", nil, err
	}
	confirmation := domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: req.SessionID, IdempotencyKey: w.ID, Acknowledged: true, Explanation: "Verified created identity and complete imports; approved catalog work " + w.ID + " in operation " + w.OperationID}
	hash, err := confirmationFingerprint(confirmation)
	if err != nil {
		return "", nil, err
	}
	result, err := confirmMappingReviewTx(ctx, tx, confirmation, hash)
	if err != nil {
		return "", nil, err
	}
	return "LINKED", &result.MappingID, nil
}
