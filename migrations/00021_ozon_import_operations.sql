-- +goose Up
-- Import operations share the existing outbox queue. Old workers must be stopped
-- or upgraded before submitting import tasks, because they only understand mock tasks.
ALTER TABLE source_imports ADD CONSTRAINT source_imports_scoped_identity UNIQUE (id, source, account_id);

CREATE TABLE ozon_import_operations (
 id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
 tenant_id text NOT NULL,
 provider text NOT NULL DEFAULT 'ozon' CHECK (provider = 'ozon'),
 account_id text NOT NULL,
 operation_type text NOT NULL CHECK (operation_type = 'OZON_CATALOG_IMPORT'),
 idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
 request_fingerprint text NOT NULL CHECK (request_fingerprint ~ '^[0-9a-f]{64}$'),
 principal_type text NOT NULL CHECK (principal_type = 'LOCAL_OPERATOR_SESSION'),
 session_audit_id text NOT NULL CHECK (length(session_audit_id) BETWEEN 1 AND 128),
 limits jsonb NOT NULL CHECK (jsonb_typeof(limits) = 'object'),
 counters jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(counters) = 'object'),
 phase text NOT NULL DEFAULT 'QUEUED' CHECK (phase IN ('QUEUED','READING_PRODUCTS','READING_WAREHOUSES','READING_STOCK','PERSISTING','COMPLETED','FAILED','CANCELLED')),
 outcome text CHECK (outcome IN ('COMPLETE_TRAVERSAL','LIMIT_REACHED','CANCELLED','API_FAILURE','INVALID_RESPONSE','PERSISTENCE_FAILURE','CONFIGURATION_UNAVAILABLE','LEASE_EXHAUSTED')),
 limiting_resource text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 attempt_started_at timestamptz,
 progress_at timestamptz,
 finished_at timestamptz,
 batch_id bigint,
 batch_created boolean,
 UNIQUE (tenant_id, idempotency_key),
 UNIQUE (id, tenant_id, account_id),
 FOREIGN KEY (tenant_id, provider, account_id) REFERENCES stock_connections(tenant_id, source, account_id) ON DELETE RESTRICT,
 FOREIGN KEY (batch_id) REFERENCES ozon_import_batches(import_id) ON DELETE RESTRICT,
 FOREIGN KEY (batch_id, provider, account_id) REFERENCES source_imports(id, source, account_id) ON DELETE RESTRICT,
 CHECK (CASE WHEN outcome = 'LIMIT_REACHED' THEN COALESCE(limiting_resource IN ('requests','attempts','pages','entities','products','warehouses','observations','response_bytes','duration'),false) ELSE limiting_resource IS NULL END),
 CHECK ((batch_id IS NULL AND batch_created IS NULL) OR (batch_id IS NOT NULL AND batch_created IS NOT NULL AND outcome = 'COMPLETE_TRAVERSAL' AND phase = 'COMPLETED')),
 CHECK (finished_at IS NULL OR started_at IS NOT NULL OR phase = 'CANCELLED')
);
CREATE INDEX ozon_import_operations_history_idx ON ozon_import_operations(tenant_id, account_id, created_at DESC, id DESC);
-- +goose StatementBegin
CREATE FUNCTION ozon_import_operation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'import operation history is immutable'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.provider,NEW.account_id,NEW.operation_type,NEW.idempotency_key,NEW.request_fingerprint,NEW.principal_type,NEW.session_audit_id,NEW.limits,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.provider,OLD.account_id,OLD.operation_type,OLD.idempotency_key,OLD.request_fingerprint,OLD.principal_type,OLD.session_audit_id,OLD.limits,OLD.created_at)
 THEN RAISE EXCEPTION 'import submission identity and limits are immutable'; END IF;
 IF OLD.finished_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'completed import operations are immutable'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER ozon_import_operation_guard BEFORE UPDATE OR DELETE ON ozon_import_operations FOR EACH ROW EXECUTE FUNCTION ozon_import_operation_guard();

ALTER TABLE outbox_tasks ALTER COLUMN observation_id DROP NOT NULL;
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_tasks_state_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_tasks_state_check CHECK (state IN ('pending','running','succeeded','failed','cancelled'));
ALTER TABLE outbox_tasks ADD COLUMN task_type text NOT NULL DEFAULT 'mock' CHECK (task_type IN ('mock','ozon_catalog_import'));
ALTER TABLE outbox_tasks ADD COLUMN import_operation_id text;
ALTER TABLE outbox_tasks ADD COLUMN import_tenant_id text;
ALTER TABLE outbox_tasks ADD COLUMN import_account_id text;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK (
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL)
 OR (task_type='ozon_catalog_import' AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL)
);
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_import_operation_fk FOREIGN KEY(import_operation_id, import_tenant_id, import_account_id)
 REFERENCES ozon_import_operations(id, tenant_id, account_id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX outbox_import_operation_unique ON outbox_tasks(import_operation_id) WHERE import_operation_id IS NOT NULL;
CREATE UNIQUE INDEX outbox_one_active_ozon_catalog ON outbox_tasks(import_tenant_id, import_account_id)
 WHERE task_type='ozon_catalog_import' AND state IN ('pending','running');
CREATE INDEX outbox_ozon_account_claim_idx ON outbox_tasks(import_account_id, available_at, id)
 WHERE task_type='ozon_catalog_import' AND state IN ('pending','running');

-- +goose StatementBegin
CREATE FUNCTION ozon_import_task_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE operation_id text; task_state text; operation_phase text; operation_outcome text; operation_batch bigint; operation_finished timestamptz;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN
  IF NEW.task_type <> 'ozon_catalog_import' THEN RETURN NULL; END IF;
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
CREATE CONSTRAINT TRIGGER ozon_import_task_consistency_on_operation AFTER INSERT OR UPDATE ON ozon_import_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ozon_import_task_consistency();
CREATE CONSTRAINT TRIGGER ozon_import_task_consistency_on_outbox AFTER INSERT OR UPDATE ON outbox_tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ozon_import_task_consistency();
-- +goose StatementBegin
CREATE FUNCTION prevent_ozon_import_task_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.task_type='ozon_catalog_import' THEN RAISE EXCEPTION 'import task history is immutable'; END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER prevent_ozon_import_task_delete BEFORE DELETE ON outbox_tasks FOR EACH ROW EXECUTE FUNCTION prevent_ozon_import_task_delete();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ozon_import_operations) THEN
  RAISE EXCEPTION 'refusing to remove durable import operation history';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX outbox_one_active_ozon_catalog;
DROP INDEX outbox_ozon_account_claim_idx;
DROP INDEX outbox_import_operation_unique;
DROP TRIGGER prevent_ozon_import_task_delete ON outbox_tasks;
DROP FUNCTION prevent_ozon_import_task_delete();
DROP TRIGGER ozon_import_task_consistency_on_outbox ON outbox_tasks;
DROP TRIGGER ozon_import_task_consistency_on_operation ON ozon_import_operations;
DROP FUNCTION ozon_import_task_consistency();
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_import_operation_fk;
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks DROP COLUMN import_account_id, DROP COLUMN import_tenant_id, DROP COLUMN import_operation_id, DROP COLUMN task_type;
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_tasks_state_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_tasks_state_check CHECK (state IN ('pending','running','succeeded','failed'));
ALTER TABLE outbox_tasks ALTER COLUMN observation_id SET NOT NULL;
DROP TABLE ozon_import_operations;
DROP FUNCTION ozon_import_operation_guard();
ALTER TABLE source_imports DROP CONSTRAINT source_imports_scoped_identity;
