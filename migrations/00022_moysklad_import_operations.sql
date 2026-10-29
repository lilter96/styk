-- +goose Up
-- Keep the historical table name and existing rows; both providers use this table.
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_provider_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT catalog_provider_check CHECK (provider IN ('ozon','moysklad'));
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_operation_type_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT catalog_type_check CHECK ((provider='ozon' AND operation_type='OZON_CATALOG_IMPORT') OR (provider='moysklad' AND operation_type='MOYSKLAD_CATALOG_IMPORT'));
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_phase_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT catalog_phase_check CHECK (phase IN ('QUEUED','READING_PRODUCTS','READING_VARIANTS','READING_WAREHOUSES','READING_STOCK','READING_AGGREGATE_STOCK','READING_WAREHOUSE_STOCK','PERSISTING','COMPLETED','FAILED','CANCELLED'));
-- The resource CHECK was unnamed in migration 21.
-- +goose StatementBegin
DO $$ DECLARE n text; BEGIN
 SELECT conname INTO n FROM pg_constraint WHERE conrelid='ozon_import_operations'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%response_bytes%';
 EXECUTE format('ALTER TABLE ozon_import_operations DROP CONSTRAINT %I', n);
END $$;
-- +goose StatementEnd
ALTER TABLE ozon_import_operations ADD CONSTRAINT import_resource_check CHECK (CASE WHEN outcome='LIMIT_REACHED' THEN COALESCE(limiting_resource IN ('requests','attempts','pages','entities','products','variants','warehouses','observations','warehouse_observations','response_bytes','duration'),false) ELSE limiting_resource IS NULL END);
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_batch_id_fkey;
-- source_imports' existing composite FK still enforces provider + account identity.
ALTER TABLE ozon_import_operations ADD CONSTRAINT catalog_operation_scope_unique UNIQUE (id,tenant_id,provider,account_id);
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_tasks_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK (task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import'));
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK (
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL));
ALTER TABLE outbox_tasks ADD COLUMN import_provider text GENERATED ALWAYS AS (CASE task_type WHEN 'ozon_catalog_import' THEN 'ozon' WHEN 'moysklad_catalog_import' THEN 'moysklad' END) STORED;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_provider_operation_fk FOREIGN KEY (import_operation_id,import_tenant_id,import_provider,import_account_id) REFERENCES ozon_import_operations(id,tenant_id,provider,account_id) ON DELETE RESTRICT;
DROP INDEX outbox_one_active_ozon_catalog;
CREATE UNIQUE INDEX outbox_one_active_catalog ON outbox_tasks(import_tenant_id,import_provider,import_account_id) WHERE task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND state IN ('pending','running');
CREATE INDEX outbox_moysklad_account_claim_idx ON outbox_tasks(import_account_id,available_at,id) WHERE task_type='moysklad_catalog_import' AND state IN ('pending','running');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ozon_import_task_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE operation_id text; task_state text; operation_phase text; operation_outcome text; operation_batch bigint; operation_finished timestamptz;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN
  IF NEW.task_type = 'mock' THEN RETURN NULL; END IF;
  operation_id := NEW.import_operation_id;
 ELSE operation_id := NEW.id; END IF;
 SELECT t.state,o.phase,o.outcome,o.batch_id,o.finished_at INTO task_state,operation_phase,operation_outcome,operation_batch,operation_finished
 FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id WHERE o.id=operation_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'import operation requires its outbox task'; END IF;
 IF task_state='succeeded' THEN
  IF operation_phase<>'COMPLETED' OR operation_outcome IS DISTINCT FROM 'COMPLETE_TRAVERSAL' OR operation_batch IS NULL OR operation_finished IS NULL THEN RAISE EXCEPTION 'successful import requires a completed batch'; END IF;
 ELSIF task_state='failed' THEN
  IF operation_phase<>'FAILED' OR operation_outcome IS NULL OR operation_batch IS NOT NULL OR operation_finished IS NULL THEN RAISE EXCEPTION 'failed import requires a terminal failure'; END IF;
 ELSIF task_state='cancelled' THEN
  IF operation_phase<>'CANCELLED' OR operation_outcome IS DISTINCT FROM 'CANCELLED' OR operation_batch IS NOT NULL OR operation_finished IS NULL THEN RAISE EXCEPTION 'cancelled import has inconsistent state'; END IF;
 ELSE
  IF operation_batch IS NOT NULL OR operation_outcome IS NOT NULL OR operation_finished IS NOT NULL OR operation_phase IN ('COMPLETED','FAILED','CANCELLED') THEN RAISE EXCEPTION 'active import cannot contain a terminal outcome'; END IF;
 END IF;
 RETURN NULL;
END $$;

-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION prevent_ozon_import_task_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.task_type IN ('ozon_catalog_import','moysklad_catalog_import') THEN RAISE EXCEPTION 'import task history is immutable'; END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION catalog_batch_eligibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.batch_id IS NOT NULL THEN
  IF NOT EXISTS (SELECT 1 FROM source_imports i JOIN stock_connections c ON c.tenant_id=NEW.tenant_id AND c.source=i.source AND c.account_id=i.account_id WHERE i.id=NEW.batch_id AND i.source=NEW.provider AND i.account_id=NEW.account_id AND (c.include_import_history OR i.started_at>=c.registered_at)) THEN RAISE EXCEPTION 'ineligible import batch'; END IF;
  IF NEW.provider='ozon' AND NOT EXISTS (SELECT 1 FROM ozon_import_batches WHERE import_id=NEW.batch_id AND traversal_status='completed') THEN RAISE EXCEPTION 'incomplete Ozon batch'; END IF;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER catalog_batch_eligibility AFTER INSERT OR UPDATE ON ozon_import_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION catalog_batch_eligibility();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ozon_import_operations WHERE provider='moysklad') THEN
 RAISE EXCEPTION 'refusing to remove durable MoySklad import history'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER catalog_batch_eligibility ON ozon_import_operations;
DROP FUNCTION catalog_batch_eligibility();
DROP INDEX outbox_moysklad_account_claim_idx;
DROP INDEX outbox_one_active_catalog;
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_provider_operation_fk;
ALTER TABLE outbox_tasks DROP COLUMN import_provider;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_tasks_task_type_check CHECK (task_type IN ('mock','ozon_catalog_import'));
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK (
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL)
 OR (task_type='ozon_catalog_import' AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL));
CREATE UNIQUE INDEX outbox_one_active_ozon_catalog ON outbox_tasks(import_tenant_id,import_account_id) WHERE task_type='ozon_catalog_import' AND state IN ('pending','running');
ALTER TABLE ozon_import_operations DROP CONSTRAINT catalog_operation_scope_unique;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_batch_id_fkey FOREIGN KEY(batch_id) REFERENCES ozon_import_batches(import_id) ON DELETE RESTRICT;
ALTER TABLE ozon_import_operations DROP CONSTRAINT catalog_provider_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_provider_check CHECK (provider='ozon');
ALTER TABLE ozon_import_operations DROP CONSTRAINT catalog_type_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_operation_type_check CHECK (operation_type='OZON_CATALOG_IMPORT');
ALTER TABLE ozon_import_operations DROP CONSTRAINT catalog_phase_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_phase_check CHECK (phase IN ('QUEUED','READING_PRODUCTS','READING_WAREHOUSES','READING_STOCK','PERSISTING','COMPLETED','FAILED','CANCELLED'));
ALTER TABLE ozon_import_operations DROP CONSTRAINT import_resource_check;
ALTER TABLE ozon_import_operations ADD CHECK (CASE WHEN outcome='LIMIT_REACHED' THEN COALESCE(limiting_resource IN ('requests','attempts','pages','entities','products','warehouses','observations','response_bytes','duration'),false) ELSE limiting_resource IS NULL END);
