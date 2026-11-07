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
	var financeCarriageVersions int
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(max(version_id),0),count(*) FILTER(WHERE version_id IN(73,74,75,76)) FROM (SELECT DISTINCT ON(version_id) version_id,is_applied FROM goose_db_version ORDER BY version_id,id DESC) versions WHERE is_applied`).Scan(&version, &financeCarriageVersions)
	if err != nil || version < 76 || financeCarriageVersions != 4 {
		return ErrSchemaIncompatible
	}
	var compatible bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('orders_automation_grants') AND conname='orders_grant_lifetime') AND to_regclass('orders_source_permissions') IS NOT NULL AND to_regclass('orders_source_refreshes') IS NOT NULL AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('connection_settings') AND attname='archived' AND NOT attisdropped) AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('orders_posting_heads') AND attname='projection' AND NOT attisdropped) AND to_regclass('provider_call_accounts') IS NOT NULL AND to_regclass('delivery_capability_observations') IS NOT NULL AND to_regclass('orders_work') IS NOT NULL AND to_regclass('orders_label_artifacts') IS NOT NULL AND to_regclass('orders_work_active_fulfillment_target') IS NOT NULL AND to_regclass('orders_native_document_intents') IS NOT NULL AND to_regclass('orders_exemplar_secrets') IS NOT NULL AND to_regclass('orders_target_fences') IS NOT NULL AND to_regclass('orders_recovery_resolutions') IS NOT NULL AND to_regclass('orders_native_point_snapshots') IS NOT NULL AND to_regclass('orders_native_point_reads') IS NOT NULL AND to_regclass('orders_native_document_snapshots') IS NOT NULL AND to_regclass('orders_native_document_reads') IS NOT NULL AND to_regclass('orders_native_document_events') IS NOT NULL AND to_regclass('orders_native_document_retries') IS NOT NULL AND to_regclass('orders_split_readbacks') IS NOT NULL AND to_regclass('orders_read_repairs') IS NOT NULL AND to_regclass('orders_read_repairs_pending') IS NOT NULL AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('orders_ingestion_leases') AND attname='attempt_started_at' AND NOT attisdropped) AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('orders_work') AND conname='orders_work_action_check' AND position('posting_country_set' in pg_get_constraintdef(oid))>0 AND position('mirror_composition_update' in pg_get_constraintdef(oid))>0 AND position('posting_split_unassembled' in pg_get_constraintdef(oid))>0)`).Scan(&compatible)
	if err != nil || !compatible {
		return ErrSchemaIncompatible
	}
	if err = s.requireProductSchema(ctx); err != nil {
		return err
	}
	if err = s.requireCommissionSchema(ctx); err != nil {
		return err
	}
	return s.requireCarriageSchema(ctx)
}

func (s *Store) requireCommissionSchema(ctx context.Context) error {
	var compatible bool
	err := s.Pool.QueryRow(ctx, `SELECT bool_and(to_regclass(name) IS NOT NULL) AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('outbox_tasks') AND conname='finance_outbox_scope_fk') FROM(VALUES('orders_merchant_intents'),('orders_finance_previews'),('orders_finance_work'),('orders_finance_periods'),('orders_finance_observations'),('orders_finance_attempts'),('orders_finance_schedule'),('orders_finance_ledgers'),('orders_finance_raw_evidence'))required(name)`).Scan(&compatible)
	if err != nil || !compatible {
		return ErrSchemaIncompatible
	}
	return nil
}

func (s *Store) requireCarriageSchema(ctx context.Context) error {
	var compatible bool
	err := s.Pool.QueryRow(ctx, `SELECT bool_and(to_regclass(name) IS NOT NULL) AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('outbox_tasks') AND conname='carriage_outbox_scope_fk') AND to_regclass('carriage_outbox_work') IS NOT NULL FROM(VALUES('orders_carriage_previews'),('orders_carriage_work'),('orders_carriage_heads'),('orders_carriage_attempts'))required(name)`).Scan(&compatible)
	if err != nil || !compatible {
		return ErrSchemaIncompatible
	}
	return nil
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
