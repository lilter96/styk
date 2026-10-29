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

func (s *Store) CatalogConfig(ctx context.Context, scope catalog.Scope) (catalogsync.Config, error) {
	if err := scope.Validate(); err != nil {
		return catalogsync.Config{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readCatalogConfig(ctx, s.Pool, scope)
}

func readCatalogConfig(ctx context.Context, q catalogQuerier, scope catalog.Scope) (catalogsync.Config, error) {
	var config catalogsync.Config
	var raw []byte
	err := q.QueryRow(ctx, `SELECT r.config FROM catalog_sync_configs c JOIN catalog_sync_config_revisions r USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.tenant_id=$1 AND c.moysklad_account=$2 AND c.ozon_account=$3`, catalogScopeArgs(scope)...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultCatalogConfig(ctx, q, scope)
	}
	if err != nil {
		return config, catalogError("read catalog configuration", err)
	}
	return config, catalogDecode(raw, &config)
}

func validateCatalogConfig(req catalogsync.SaveConfigRequest) error {
	if err := req.Config.Validate(); err != nil {
		return err
	}
	if req.ExpectedRevision < 0 || req.Actor == "" || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 {
		return catalogsync.ErrInvalid
	}
	if (req.Config.Enabled || req.Config.TestEnabled) && !catalogTestScope(req.Config.Scope) {
		return catalogsync.ErrBlocked
	}
	return nil
}

func (s *Store) SaveCatalogConfig(ctx context.Context, req catalogsync.SaveConfigRequest, now time.Time) (catalogsync.Config, error) {
	if err := validateCatalogConfig(req); err != nil {
		return catalogsync.Config{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return catalogsync.Config{}, fmt.Errorf("begin catalog configuration: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, req.Config.Scope.Tenant, req.Actor); err != nil {
		return catalogsync.Config{}, err
	}
	if err = catalogLock(ctx, tx, req.Config.Scope); err != nil {
		return catalogsync.Config{}, err
	}
	hash, err := catalogConfigHash(req)
	if err != nil {
		return catalogsync.Config{}, err
	}
	previous, err := replayCatalogConfig(ctx, tx, req, hash)
	if err == nil {
		return previous, nil
	}
	if !errors.Is(err, catalogsync.ErrNotFound) {
		return catalogsync.Config{}, err
	}
	config, err := writeCatalogConfig(ctx, tx, req, hash, now)
	if err != nil {
		return config, err
	}
	if err = tx.Commit(ctx); err != nil {
		return config, fmt.Errorf("commit catalog configuration: %w", err)
	}
	return config, nil
}

func replayCatalogConfig(ctx context.Context, tx pgx.Tx, req catalogsync.SaveConfigRequest, hash string) (catalogsync.Config, error) {
	var raw []byte
	var previous string
	var config catalogsync.Config
	args := append(catalogScopeArgs(req.Config.Scope), req.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT config,request_hash FROM catalog_sync_config_revisions WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND request_key=$4`, args...).Scan(&raw, &previous)
	if err != nil {
		return config, catalogError("read catalog configuration replay", err)
	}
	if previous != hash {
		return config, catalogsync.ErrConflict
	}
	return config, catalogDecode(raw, &config)
}

func writeCatalogConfig(ctx context.Context, tx pgx.Tx, req catalogsync.SaveConfigRequest, hash string, now time.Time) (catalogsync.Config, error) {
	c := req.Config
	var revision int64
	err := tx.QueryRow(ctx, `SELECT revision FROM catalog_sync_configs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 FOR UPDATE`, catalogScopeArgs(c.Scope)...).Scan(&revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, fmt.Errorf("lock catalog configuration: %w", err)
	}
	if revision != req.ExpectedRevision {
		return c, catalogsync.ErrStale
	}
	c.Revision = revision + 1
	c.UpdatedAt = now.UTC()
	mappings, err := catalogMappings(ctx, tx, c.Scope, c.Targets)
	if err != nil {
		return c, err
	}
	if err = validateCatalogCreations(ctx, tx, c.Scope, c.Targets); err != nil {
		return c, err
	}
	raw, err := catalogJSON(c)
	if err != nil {
		return c, err
	}
	args := append(catalogScopeArgs(c.Scope), c.Revision, c.Enabled, now)
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_configs(tenant_id,moysklad_account,ozon_account,revision,enabled,next_run_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET revision=EXCLUDED.revision,enabled=EXCLUDED.enabled,next_run_at=EXCLUDED.next_run_at,updated_at=EXCLUDED.updated_at`, args...); err != nil {
		return c, fmt.Errorf("save catalog configuration head: %w", err)
	}
	args = append(catalogScopeArgs(c.Scope), c.Revision, raw, req.Actor, req.IdempotencyKey, hash, now)
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_config_revisions(tenant_id,moysklad_account,ozon_account,revision,config,actor,request_key,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, args...); err != nil {
		return c, fmt.Errorf("save catalog configuration revision: %w", err)
	}
	for _, e := range c.Targets {
		if err = saveCatalogEnrollment(ctx, tx, c, e, mappings[e.MappingID]); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (s *Store) DueCatalogConfigs(ctx context.Context, now time.Time, limit int) ([]catalogsync.Config, error) {
	if limit < 1 || limit > 100 {
		return nil, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin catalog scheduler: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	rows, err := tx.Query(ctx, `SELECT r.config FROM catalog_sync_configs c JOIN catalog_sync_config_revisions r USING(tenant_id,moysklad_account,ozon_account,revision) WHERE c.enabled AND c.next_run_at<=$1 ORDER BY c.next_run_at,c.tenant_id LIMIT $2 FOR UPDATE OF c SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due catalog configurations: %w", err)
	}
	configs, err := scanCatalogConfigs(rows)
	if err != nil {
		return nil, err
	}
	for _, c := range configs {
		args := append(catalogScopeArgs(c.Scope), now.Add(time.Duration(c.IntervalSeconds)*time.Second))
		if _, err = tx.Exec(ctx, `UPDATE catalog_sync_configs SET next_run_at=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, args...); err != nil {
			return nil, fmt.Errorf("schedule next catalog run: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit catalog scheduler: %w", err)
	}
	return configs, nil
}

func scanCatalogConfigs(rows pgx.Rows) ([]catalogsync.Config, error) {
	defer rows.Close()
	configs := make([]catalogsync.Config, 0, 10)
	for rows.Next() {
		var raw []byte
		var c catalogsync.Config
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan catalog configuration: %w", err)
		}
		if err := catalogDecode(raw, &c); err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read due catalog configurations: %w", err)
	}
	return configs, nil
}

func catalogConfigHash(req catalogsync.SaveConfigRequest) (string, error) {
	return catalogHash(struct {
		Request catalogsync.SaveConfigRequest
		Actor   string
	}{req, req.Actor})
}

func defaultCatalogConfig(ctx context.Context, q catalogQuerier, scope catalog.Scope) (catalogsync.Config, error) {
	var owned int
	err := q.QueryRow(ctx, `SELECT count(*) FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, catalogScopeArgs(scope)...).Scan(&owned)
	if err != nil {
		return catalogsync.Config{}, fmt.Errorf("check catalog setup ownership: %w", err)
	}
	if owned != 2 {
		return catalogsync.Config{}, catalogsync.ErrNotFound
	}
	return catalogsync.Config{Scope: scope, IntervalSeconds: 300, Rules: make(catalog.Policies), Targets: make([]catalogsync.Enrollment, 0)}, nil
}
