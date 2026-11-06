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

func (s *Store) CatalogImportContext(ctx context.Context, scope catalog.Scope, actor string, targets []catalogsync.Enrollment) (catalogsync.ReviewContext, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return catalogsync.ReviewContext{}, fmt.Errorf("begin import context: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, scope.Tenant, actor); err != nil {
		return catalogsync.ReviewContext{}, err
	}
	if err = catalogLock(ctx, tx, scope); err != nil {
		return catalogsync.ReviewContext{}, err
	}
	config, err := readCatalogConfig(ctx, tx, scope)
	if err != nil {
		return catalogsync.ReviewContext{}, err
	}
	config, err = initializeImportConfig(ctx, tx, config, actor)
	if err != nil {
		return catalogsync.ReviewContext{}, err
	}
	credentials, err := catalogCredentials(ctx, tx, scope)
	if err != nil {
		return catalogsync.ReviewContext{}, err
	}
	contextTargets, err := catalogImportTargets(ctx, tx, scope, targets)
	if err != nil {
		return catalogsync.ReviewContext{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return catalogsync.ReviewContext{}, fmt.Errorf("commit import context: %w", err)
	}
	return catalogsync.ReviewContext{Config: config, Credentials: credentials, Targets: contextTargets}, nil
}

func catalogImportReview(ctx context.Context, tx pgx.Tx, p catalogsync.Preview, current catalogsync.ReviewContext) (catalogsync.ReviewContext, error) {
	if len(p.ImportTargets) == 0 {
		return current, nil
	}
	if err := p.ValidateImportTargets(); err != nil {
		return current, err
	}
	if err := catalogMappingLocks(ctx, tx, p.Scope); err != nil {
		return current, err
	}
	evidence, err := catalogTargetEvidence(ctx, tx, p.Scope, p.ImportTargets)
	if err != nil {
		return current, err
	}
	current.Config.Targets = p.ImportTargets
	current.Targets = make([]catalogsync.TargetContext, 0, len(p.ImportTargets))
	for i, e := range p.ImportTargets {
		t := evidence[e.ID]
		t.Enrollment = e
		if i >= len(p.Decisions) || p.Decisions[i].EnrollmentID != e.ID {
			return current, catalogsync.ErrInvalid
		}
		if p.Decisions[i].Ready {
			if err := catalogImportSourceCurrent(ctx, tx, p.Scope, e); err != nil {
				return current, err
			}
			if err := catalogImportUnresolved(ctx, tx, p.Scope, e); err != nil {
				return current, err
			}
		}
		if p.Decisions[i].Ready && (t.Fenced || t.CreatedTarget != nil) {
			return current, catalogsync.ErrStale
		}
		current.Targets = append(current.Targets, t)
	}
	return current, nil
}

func catalogImportSourceCurrent(ctx context.Context, q catalogQuerier, scope catalog.Scope, e catalogsync.Enrollment) error {
	var present bool
	err := q.QueryRow(ctx, `WITH latest AS (
 SELECT i.id,i.finished_at,b.traversal_status FROM source_imports i
 JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id
 LEFT JOIN ozon_import_batches b ON b.import_id=i.id
 WHERE i.source=$4 AND i.account_id=$5 AND (c.include_import_history OR i.started_at>=c.registered_at)
 ORDER BY i.id DESC LIMIT 1)
 SELECT EXISTS(SELECT 1 FROM latest l WHERE l.finished_at BETWEEN clock_timestamp()-interval '1 hour' AND clock_timestamp()
 AND (($4='ozon' AND l.traversal_status='completed' AND EXISTS(SELECT 1 FROM ozon_product_observations p WHERE p.import_id=l.id AND p.product_id=$6 AND p.offer_id=$7 AND p.archived=false))
 OR ($4='moysklad' AND EXISTS(SELECT 1 FROM source_entities e WHERE e.import_id=l.id AND e.external_id=$6 AND e.kind=$8 AND e.kind='product' AND e.archived=false))))
 AND NOT EXISTS(SELECT 1 FROM product_mappings m WHERE m.tenant_id=$1 AND m.ozon_account=$3 AND (($4='ozon' AND m.ozon_product_id=$6) OR ($4='moysklad' AND m.moysklad_account=$2 AND m.target_kind=$8 AND m.target_id=$6)))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, e.Source.Account.Provider, e.Source.Account.ID, e.Source.ID, e.Source.OfferID, e.Source.EntityType).Scan(&present)
	if err != nil {
		return fmt.Errorf("validate import source: %w", err)
	}
	if !present {
		return catalogsync.ErrStale
	}
	return nil
}

// Check every durable creation identity for this source.
// A changed browser key must never bypass an unresolved or completed write.
func catalogImportUnresolved(ctx context.Context, q catalogQuerier, scope catalog.Scope, e catalogsync.Enrollment) error {
	var existing bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_sync_associations a WHERE a.tenant_id=$1 AND a.target_account=$2 AND a.target_provider=$3 AND a.source_provider=$4 AND a.source_account=$5 AND a.source_kind=$6 AND a.source_id=$7 AND (a.target_key<>$8 OR a.resolved_identity IS NOT NULL OR EXISTS(SELECT 1 FROM catalog_sync_work w WHERE w.tenant_id=a.tenant_id AND w.moysklad_account=a.moysklad_account AND w.ozon_account=a.ozon_account AND w.target_key=a.target_key AND (w.state NOT IN ('BLOCKED','REJECTED') OR w.dispatch_count>0))))`, scope.Tenant, e.Target.Account.ID, e.Target.Account.Provider, e.Source.Account.Provider, e.Source.Account.ID, e.Source.EntityType, e.Source.ID, e.ID).Scan(&existing)
	if err != nil {
		return fmt.Errorf("check prior import outcomes: %w", err)
	}
	if existing {
		return catalogsync.ErrConflict
	}
	return nil
}

func saveCatalogImportEnrollments(ctx context.Context, tx pgx.Tx, p catalogsync.Preview, c catalogsync.Config, decisions []catalogsync.Decision) error {
	if len(p.ImportTargets) == 0 {
		return nil
	}
	chosen := make(map[string]bool, len(decisions))
	for _, d := range decisions {
		chosen[d.EnrollmentID] = true
	}
	for _, e := range p.ImportTargets {
		if !chosen[e.ID] {
			continue
		}
		if err := saveCatalogImportEnrollment(ctx, tx, c, e); err != nil {
			return err
		}
	}
	return nil
}

func catalogImportDispatchCurrent(ctx context.Context, tx pgx.Tx, w catalogsync.Work) (bool, error) {
	if w.Automatic || w.Command.Action != catalog.ActionCreate || !catalogScopeValid(w.Scope) {
		return false, nil
	}
	var previewID string
	err := tx.QueryRow(ctx, `SELECT preview_id FROM catalog_sync_operations WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4`, w.OperationID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount).Scan(&previewID)
	if err != nil {
		return false, fmt.Errorf("load import authorization: %w", err)
	}
	p, err := readCatalogPreview(ctx, tx, w.Scope, previewID)
	if err != nil {
		return false, err
	}
	if p.ValidateImportTargets() != nil || p.ConfigRevision != w.ConfigRevision {
		return false, nil
	}
	credentials, err := catalogCredentials(ctx, tx, w.Scope)
	if err != nil || credentials != w.Credentials {
		return false, err
	}
	for _, e := range p.ImportTargets {
		if e.ID != w.EnrollmentID {
			continue
		}
		return catalogImportTargetDispatchable(ctx, tx, w, e)
	}
	return false, nil
}

func catalogImportTargets(ctx context.Context, tx pgx.Tx, scope catalog.Scope, targets []catalogsync.Enrollment) ([]catalogsync.TargetContext, error) {
	if err := catalogMappingLocks(ctx, tx, scope); err != nil {
		return nil, err
	}
	evidence, err := catalogTargetEvidence(ctx, tx, scope, targets)
	if err != nil {
		return nil, err
	}
	result := make([]catalogsync.TargetContext, 0, len(targets))
	for _, e := range targets {
		t := evidence[e.ID]
		t.Enrollment = e
		err := catalogImportSourceCurrent(ctx, tx, scope, e)
		if errors.Is(err, catalogsync.ErrStale) {
			t.BlockCode = "IMPORT_SOURCE_NOT_ELIGIBLE"
		} else if err != nil {
			return nil, err
		}
		err = catalogImportUnresolved(ctx, tx, scope, e)
		if errors.Is(err, catalogsync.ErrConflict) {
			t.BlockCode = "TARGET_OUTCOME_UNRESOLVED"
		} else if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}

func readCatalogPreviewContext(ctx context.Context, tx pgx.Tx, p catalogsync.Preview) (catalogsync.ReviewContext, error) {
	if len(p.ImportTargets) == 0 {
		return readCatalogReviewContext(ctx, tx, p.Scope)
	}
	var current catalogsync.ReviewContext
	var err error
	current.Config, err = readCatalogConfig(ctx, tx, p.Scope)
	if err != nil {
		return current, err
	}
	current.Credentials, err = catalogCredentials(ctx, tx, p.Scope)
	return current, err
}

func saveCatalogImportEnrollment(ctx context.Context, tx pgx.Tx, c catalogsync.Config, e catalogsync.Enrollment) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_sync_enrollments WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND config_revision=$4 AND target_key=$5)`, c.Scope.Tenant, c.Scope.MoyskladAccount, c.Scope.OzonAccount, c.Revision, e.ID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("read import enrollment: %w", err)
	}
	if exists {
		return checkCatalogAssociation(ctx, tx, c.Scope, e)
	}
	return saveCatalogEnrollment(ctx, tx, c, e, catalog.Mapping{})
}

func initializeImportConfig(ctx context.Context, tx pgx.Tx, c catalogsync.Config, actor string) (catalogsync.Config, error) {
	if c.Revision != 0 {
		return c, nil
	}
	req := catalogsync.SaveConfigRequest{Config: c, Actor: actor, IdempotencyKey: "initialize-import"}
	hash, err := catalogConfigHash(req)
	if err != nil {
		return c, err
	}
	return writeCatalogConfig(ctx, tx, req, hash, time.Now().UTC())
}

func catalogImportTargetDispatchable(ctx context.Context, tx pgx.Tx, w catalogsync.Work, e catalogsync.Enrollment) (bool, error) {
	if err := catalogCreationMatches(w.Command, e); err != nil {
		return false, nil
	}
	if err := catalogImportSourceCurrent(ctx, tx, w.Scope, e); err != nil {
		if errors.Is(err, catalogsync.ErrStale) {
			return false, nil
		}
		return false, err
	}
	evidence, err := catalogTargetEvidence(ctx, tx, w.Scope, []catalogsync.Enrollment{e})
	if err != nil {
		return false, err
	}
	target := evidence[e.ID]
	target.Enrollment = e
	return catalogCreationDispatchable(ctx, tx, w.Scope, target)
}
