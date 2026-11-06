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
	if err != nil || version < 71 {
		return ErrSchemaIncompatible
	}
	var compatible bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('orders_automation_grants') AND conname='orders_grant_lifetime') AND to_regclass('orders_source_permissions') IS NOT NULL AND to_regclass('orders_source_refreshes') IS NOT NULL AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('connection_settings') AND attname='archived' AND NOT attisdropped) AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('orders_posting_heads') AND attname='projection' AND NOT attisdropped) AND to_regclass('provider_call_accounts') IS NOT NULL AND to_regclass('delivery_capability_observations') IS NOT NULL AND to_regclass('orders_work') IS NOT NULL AND to_regclass('orders_label_artifacts') IS NOT NULL AND to_regclass('orders_work_active_fulfillment_target') IS NOT NULL AND to_regclass('orders_native_document_intents') IS NOT NULL AND to_regclass('orders_exemplar_secrets') IS NOT NULL AND to_regclass('orders_target_fences') IS NOT NULL AND to_regclass('orders_recovery_resolutions') IS NOT NULL AND to_regclass('orders_native_point_snapshots') IS NOT NULL AND to_regclass('orders_native_point_reads') IS NOT NULL AND to_regclass('orders_native_document_snapshots') IS NOT NULL AND to_regclass('orders_native_document_reads') IS NOT NULL AND to_regclass('orders_native_document_events') IS NOT NULL AND to_regclass('orders_native_document_retries') IS NOT NULL AND to_regclass('orders_split_readbacks') IS NOT NULL AND to_regclass('orders_read_repairs') IS NOT NULL AND to_regclass('orders_read_repairs_pending') IS NOT NULL AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('orders_ingestion_leases') AND attname='attempt_started_at' AND NOT attisdropped) AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('orders_work') AND conname='orders_work_action_check' AND position('posting_country_set' in pg_get_constraintdef(oid))>0 AND position('mirror_composition_update' in pg_get_constraintdef(oid))>0 AND position('posting_split_unassembled' in pg_get_constraintdef(oid))>0)`).Scan(&compatible)
	if err != nil || !compatible {
		return ErrSchemaIncompatible
	}
	return s.requireProductSchema(ctx)
}

func (s *Store) requireProductSchema(ctx context.Context) error {
	var compatible bool
	err := s.Pool.QueryRow(ctx, `SELECT bool_and(to_regclass(name) IS NOT NULL)
 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('source_entities') AND attname='archived' AND NOT attisdropped)
 FROM (VALUES ('product_matching_policies'),('product_matching_policy_audits'),('product_matching_previews'),('product_matching_runs'),('product_matching_cycles'),('catalog_created_links'),('product_mapping_replacements')) AS required(name)`).Scan(&compatible)
	if err != nil || !compatible {
		return ErrSchemaIncompatible
	}
	return nil
}
