package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func (s *Store) CatalogReviewContext(ctx context.Context, scope catalog.Scope) (catalogsync.ReviewContext, error) {
	if err := scope.Validate(); err != nil {
		return catalogsync.ReviewContext{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return catalogsync.ReviewContext{}, fmt.Errorf("begin catalog context: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	result, err := readCatalogReviewContext(ctx, tx, scope)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit catalog context: %w", err)
	}
	return result, nil
}

func readCatalogReviewContext(ctx context.Context, q catalogQuerier, scope catalog.Scope) (catalogsync.ReviewContext, error) {
	var result catalogsync.ReviewContext
	var err error
	result.Config, err = readCatalogConfig(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Credentials, err = catalogCredentials(ctx, q, scope)
	if err != nil {
		return result, err
	}
	mappings, err := catalogMappings(ctx, q, scope, result.Config.Targets)
	if err != nil {
		return result, err
	}
	evidence, err := catalogTargetEvidence(ctx, q, scope, result.Config.Targets)
	if err != nil {
		return result, err
	}
	result.Targets = make([]catalogsync.TargetContext, 0, len(result.Config.Targets))
	for _, e := range result.Config.Targets {
		target := evidence[e.ID]
		target.Enrollment = e
		if e.Create == nil {
			m := mappings[e.MappingID]
			if e.Source.Account.Provider == catalog.ProviderOzon {
				m.Source, m.Target = m.Target, m.Source
			}
			target.Mapping = &m
		}
		result.Targets = append(result.Targets, target)
	}
	return result, nil
}

func catalogTargetEvidence(ctx context.Context, q catalogQuerier, scope catalog.Scope, targets []catalogsync.Enrollment) (map[string]catalogsync.TargetContext, error) {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	args := append(catalogScopeArgs(scope), ids)
	rows, err := q.Query(ctx, `SELECT a.target_key,a.resolved_identity,b.baseline,EXISTS(SELECT 1 FROM catalog_sync_work w WHERE w.tenant_id=a.tenant_id AND w.moysklad_account=a.moysklad_account AND w.ozon_account=a.ozon_account AND w.target_key=a.target_key AND w.state IN ('QUEUED','CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED','UNKNOWN')) FROM catalog_sync_associations a LEFT JOIN LATERAL(SELECT baseline FROM catalog_sync_baselines b WHERE b.tenant_id=a.tenant_id AND b.moysklad_account=a.moysklad_account AND b.ozon_account=a.ozon_account AND b.target_key=a.target_key ORDER BY b.created_at DESC,b.work_id DESC LIMIT 1) b ON true WHERE a.tenant_id=$1 AND a.moysklad_account=$2 AND a.ozon_account=$3 AND a.target_key=ANY($4)`, args...)
	if err != nil {
		return nil, fmt.Errorf("load catalog target evidence: %w", err)
	}
	defer rows.Close()
	result := make(map[string]catalogsync.TargetContext, len(targets))
	for rows.Next() {
		var id string
		var created, baseline []byte
		var target catalogsync.TargetContext
		if err = rows.Scan(&id, &created, &baseline, &target.Fenced); err != nil {
			return nil, fmt.Errorf("scan catalog target evidence: %w", err)
		}
		if created != nil {
			target.CreatedTarget = new(catalog.Identity)
			if err = catalogDecode(created, target.CreatedTarget); err != nil {
				return nil, err
			}
		}
		if baseline != nil {
			target.Baseline = new(catalog.Baseline)
			if err = catalogDecode(baseline, target.Baseline); err != nil {
				return nil, err
			}
		}
		result[id] = target
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read catalog target evidence: %w", err)
	}
	return result, nil
}

func (s *Store) CatalogPreview(ctx context.Context, scope catalog.Scope, id string) (catalogsync.Preview, error) {
	if scope.Validate() != nil || id == "" {
		return catalogsync.Preview{}, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readCatalogPreview(ctx, s.Pool, scope, id)
}

func readCatalogPreview(ctx context.Context, q catalogQuerier, scope catalog.Scope, id string) (catalogsync.Preview, error) {
	var p catalogsync.Preview
	var raw []byte
	args := append(catalogScopeArgs(scope), id)
	err := q.QueryRow(ctx, `SELECT preview,actor FROM catalog_sync_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, args...).Scan(&raw, &p.Actor)
	if err != nil {
		return p, catalogError("read catalog preview", err)
	}
	return p, catalogDecode(raw, &p)
}

func (s *Store) SaveCatalogPreview(ctx context.Context, p catalogsync.Preview) (catalogsync.Preview, error) {
	if p.Scope.Validate() != nil || p.ID == "" || len(p.Decisions) > 1000 || !p.ExpiresAt.After(p.CreatedAt) {
		return p, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return p, fmt.Errorf("begin catalog preview: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, p.Scope.Tenant, p.Actor); err != nil {
		return p, err
	}
	if err = catalogLock(ctx, tx, p.Scope); err != nil {
		return p, err
	}
	if err = catalogCredentialLocks(ctx, tx, p.Scope); err != nil {
		return p, err
	}
	current, err := readCatalogReviewContext(ctx, tx, p.Scope)
	if err != nil {
		return p, err
	}
	if current.Config.Revision != p.ConfigRevision || current.Credentials != p.Credentials {
		return p, catalogsync.ErrStale
	}
	if err = validateCatalogPreview(p, current); err != nil {
		return p, err
	}
	if err = insertCatalogPreview(ctx, tx, p); err != nil {
		return p, err
	}
	if err = tx.Commit(ctx); err != nil {
		return p, fmt.Errorf("commit catalog preview: %w", err)
	}
	return p, nil
}

func insertCatalogPreview(ctx context.Context, tx pgx.Tx, p catalogsync.Preview) error {
	raw, err := catalogJSON(p)
	if err != nil {
		return err
	}
	hash, err := catalogHash(p)
	if err != nil {
		return err
	}
	args := []any{p.ID, p.Scope.Tenant, p.Scope.MoyskladAccount, p.Scope.OzonAccount, p.ConfigRevision, raw, hash, p.Actor, p.CreatedAt, p.ExpiresAt}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, args...); err != nil {
		return catalogError("save catalog preview", err)
	}
	for _, d := range p.Decisions {
		for _, product := range []catalog.Product{d.Source, d.Target} {
			if product.Identity.ID == "" || product.Revision == "" || product.ObservedAt.IsZero() {
				continue
			}
			id, err := insertCatalogSnapshot(ctx, tx, p.Scope, product)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_preview_snapshots(preview_id,tenant_id,moysklad_account,ozon_account,config_revision,snapshot_id,provider,account_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.ID, p.Scope.Tenant, p.Scope.MoyskladAccount, p.Scope.OzonAccount, p.ConfigRevision, id, product.Identity.Account.Provider, product.Identity.Account.ID); err != nil {
				return fmt.Errorf("link canonical catalog snapshot: %w", err)
			}
		}
	}
	return nil
}

func insertCatalogSnapshot(ctx context.Context, tx pgx.Tx, scope catalog.Scope, p catalog.Product) (int64, error) {
	if !scope.Contains(p.Identity.Account) || p.Identity.Validate() != nil || p.Revision == "" || p.ObservedAt.IsZero() {
		return 0, catalogsync.ErrInvalid
	}
	raw, err := catalogJSON(p)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO catalog_sync_snapshots(tenant_id,provider,account_id,entity_kind,entity_id,revision,product,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, scope.Tenant, p.Identity.Account.Provider, p.Identity.Account.ID, p.Identity.EntityType, p.Identity.ID, p.Revision, raw, p.ObservedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save canonical catalog snapshot: %w", err)
	}
	return id, nil
}

func validateCatalogPreview(p catalogsync.Preview, current catalogsync.ReviewContext) error {
	targets := make(map[string]catalogsync.TargetContext, len(current.Targets))
	seen := make(map[string]bool, len(p.Decisions))
	for _, t := range current.Targets {
		targets[t.Enrollment.ID] = t
	}
	for _, d := range p.Decisions {
		target, ok := targets[d.EnrollmentID]
		if !ok || seen[d.EnrollmentID] {
			return catalogsync.ErrInvalid
		}
		seen[d.EnrollmentID] = true
		if !d.Ready && len(d.Commands) == 0 && len(d.Validation) > 0 {
			continue
		}
		if !target.Enrollment.Source.SameEntity(d.Source.Identity) || !p.Scope.Contains(d.Target.Identity.Account) {
			return catalogsync.ErrStale
		}
		if target.Mapping != nil {
			if d.Mapping == nil || *d.Mapping != *target.Mapping {
				return catalogsync.ErrStale
			}
		}
		if target.CreatedTarget != nil && !target.CreatedTarget.SameEntity(d.Target.Identity) {
			return catalogsync.ErrStale
		}
		if target.Enrollment.Create == nil && !target.Enrollment.Target.SameEntity(d.Target.Identity) {
			return catalogsync.ErrStale
		}
		if err := validateCatalogDecision(p.Scope, target.Enrollment, d); err != nil {
			return err
		}
	}
	return nil
}

func validateCatalogDecision(scope catalog.Scope, e catalogsync.Enrollment, d catalogsync.Decision) error {
	if d.Ready && (len(d.Validation) != 0 || len(d.Plan.Conflicts) != 0) {
		return catalogsync.ErrInvalid
	}
	for _, cmd := range d.Commands {
		if err := catalog.ValidateCommand(cmd); err != nil {
			return err
		}
		if cmd.Scope != scope || cmd.MappingID != e.MappingID || cmd.MappingVersion != e.MappingVersion {
			return catalogsync.ErrStale
		}
		source, target := d.Source, d.Target
		if cmd.Source.Account == target.Identity.Account {
			source, target = target, source
		}
		if !cmd.Source.SameEntity(source.Identity) || cmd.ExpectedSourceRevision != source.Revision || cmd.ExpectedTargetRevision != target.Revision {
			return catalogsync.ErrStale
		}
		if cmd.Action == catalog.ActionCreate {
			if e.Create == nil || !d.TargetAbsent || cmd.Target != e.Target {
				return catalogsync.ErrStale
			}
			if err := catalogCreationMatches(cmd, e); err != nil {
				return err
			}
			continue
		}
		if !cmd.Target.SameEntity(target.Identity) {
			return catalogsync.ErrStale
		}
		if err := catalogCommandMatchesPlan(cmd, d.Plan); err != nil {
			return err
		}
	}
	return nil
}

func catalogCommandMatchesPlan(cmd catalog.Command, plan catalog.Plan) error {
	if len(cmd.Extensions) > 0 || cmd.Action != catalog.ActionUpdate {
		return catalogsync.ErrInvalid
	}
	for field, value := range cmd.Values {
		found := false
		for _, change := range plan.Changes {
			if change.Field == field && change.Target.SameEntity(cmd.Target) && catalog.EqualValue(change.After, value) {
				found = true
				break
			}
		}
		if !found {
			return catalogsync.ErrStale
		}
	}
	return nil
}

func catalogCreationMatches(cmd catalog.Command, e catalogsync.Enrollment) error {
	expected := cmd
	expected.Values = e.Create.Values
	expected.Extensions = e.Create.Extensions
	expected.Parent = e.Create.Parent
	expected.CreateID = e.Create.ID
	expected.CreateExternalCode = e.Create.ExternalCode
	actualHash, err := catalog.CommandFingerprint(cmd)
	if err != nil {
		return err
	}
	expectedHash, err := catalog.CommandFingerprint(expected)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return catalogsync.ErrStale
	}
	return nil
}
