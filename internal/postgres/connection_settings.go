package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
)

func (s *Store) ConnectionSettings(ctx context.Context, scope credentials.Scope) (domain.ConnectionSettings, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var settings domain.ConnectionSettings
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(p.name,k.name,'Основной аккаунт'),COALESCE(p.description,''),COALESCE(p.revision,0)
	 FROM stock_connections c LEFT JOIN connection_settings p USING(tenant_id,source,account_id)
	 LEFT JOIN connection_credentials k ON k.tenant_id=c.tenant_id AND k.provider=c.source AND k.account_id=c.account_id
	 WHERE c.tenant_id=$1 AND c.source=$2 AND c.account_id=$3 AND NOT COALESCE(p.archived,false)`, scope.Tenant, scope.Provider, scope.Account).Scan(&settings.Name, &settings.Description, &settings.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return settings, credentials.ErrNotFound
	}
	if err != nil {
		return settings, fmt.Errorf("read connection settings: %w", err)
	}
	return settings, nil
}

func (s *Store) SaveConnectionSettings(ctx context.Context, scope credentials.Scope, settings domain.ConnectionSettings, actor string) (domain.ConnectionSettings, error) {
	if !scope.Valid() || scope.Provider != "moysklad" || !settings.IsValid() || actor == "" {
		return domain.ConnectionSettings{}, credentials.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.ConnectionSettings{}, fmt.Errorf("begin connection settings: %w", err)
	}
	defer rollbackTransaction(tx)
	if err = requireMerchantActor(ctx, tx, scope.Tenant, actor, true); err != nil {
		return domain.ConnectionSettings{}, fmt.Errorf("authorize connection settings: %w", err)
	}
	if err = lockConnectionSettings(ctx, tx, scope); err != nil {
		return domain.ConnectionSettings{}, err
	}
	saved, err := saveConnectionSettings(ctx, tx, scope, settings, actor)
	if err != nil {
		return domain.ConnectionSettings{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ConnectionSettings{}, fmt.Errorf("commit connection settings: %w", err)
	}
	return saved, nil
}

func lockConnectionSettings(ctx context.Context, tx pgx.Tx, scope credentials.Scope) error {
	var owner string
	err := tx.QueryRow(ctx, `SELECT tenant_id FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3 FOR UPDATE`, scope.Tenant, scope.Provider, scope.Account).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return credentials.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock connection settings: %w", err)
	}
	return nil
}

func saveConnectionSettings(ctx context.Context, tx pgx.Tx, scope credentials.Scope, settings domain.ConnectionSettings, actor string) (domain.ConnectionSettings, error) {
	var current int64
	err := tx.QueryRow(ctx, `SELECT revision FROM connection_settings WHERE tenant_id=$1 AND source=$2 AND account_id=$3`, scope.Tenant, scope.Provider, scope.Account).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ConnectionSettings{}, fmt.Errorf("read settings revision: %w", err)
	}
	if current != settings.Revision {
		return domain.ConnectionSettings{}, credentials.ErrVersion
	}
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT archived FROM connection_settings WHERE tenant_id=$1 AND source=$2 AND account_id=$3),false)`, scope.Tenant, scope.Provider, scope.Account).Scan(&archived); err != nil {
		return domain.ConnectionSettings{}, fmt.Errorf("read connection archive state: %w", err)
	}
	if archived {
		return domain.ConnectionSettings{}, credentials.ErrNotFound
	}
	err = tx.QueryRow(ctx, `INSERT INTO connection_settings(tenant_id,source,account_id,name,description,revision,updated_by)
	 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,source,account_id) DO UPDATE
	 SET name=EXCLUDED.name,description=EXCLUDED.description,revision=EXCLUDED.revision,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()
	 RETURNING name,description,revision`, scope.Tenant, scope.Provider, scope.Account, settings.Name, settings.Description, current+1, actor).Scan(&settings.Name, &settings.Description, &settings.Revision)
	if err != nil {
		return domain.ConnectionSettings{}, fmt.Errorf("save connection settings: %w", err)
	}
	return settings, nil
}
