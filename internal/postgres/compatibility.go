package postgres

import (
	"context"
	"errors"
)

var ErrSchemaIncompatible = errors.New("database schema is incompatible: apply current migrations before starting STYK")

// RequireCurrentSchema prevents a healthy TCP connection from making an old or
// partially upgraded database appear ready. Open itself remains usable by the
// migration/bootstrap tools on an empty database.
func (s *Store) RequireCurrentSchema(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var version int64
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(max(version_id),0) FROM (SELECT DISTINCT ON(version_id) version_id,is_applied FROM goose_db_version ORDER BY version_id,id DESC) versions WHERE is_applied`).Scan(&version)
	if err != nil || version < 48 {
		return ErrSchemaIncompatible
	}
	var compatible bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('orders_posting_heads') AND attname='projection' AND NOT attisdropped) AND to_regclass('provider_call_accounts') IS NOT NULL AND to_regclass('delivery_capability_observations') IS NOT NULL AND to_regclass('orders_work') IS NOT NULL`).Scan(&compatible)
	if err != nil || !compatible {
		return ErrSchemaIncompatible
	}
	return nil
}
