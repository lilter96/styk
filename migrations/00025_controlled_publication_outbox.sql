-- +goose Up
-- Controlled real-account dispatch has its own non-retryable outbox entry.
-- Existing simulator publications remain valid and do not gain implicit work.
ALTER TABLE outbox_tasks ADD COLUMN publication_operation_id text REFERENCES stock_publications(id) ON DELETE RESTRICT;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK (task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication'));
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK (
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL AND publication_operation_id IS NULL)
 OR (task_type='stock_publication' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NOT NULL AND max_attempts=1 AND attempts<=1));
CREATE UNIQUE INDEX outbox_publication_operation_unique ON outbox_tasks(publication_operation_id) WHERE publication_operation_id IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ozon_import_task_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE operation_id text; task_state text; operation_phase text; operation_outcome text; operation_batch bigint; operation_finished timestamptz;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN
  IF NEW.task_type NOT IN ('ozon_catalog_import','moysklad_catalog_import') THEN RETURN NULL; END IF;
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
  IF operation_phase<>'CANCELLED' OR operation_outcome IS DISTINCT FROM 'CANCELLED' OR operation_batch IS NOT NULL OR operation_finished IS NULL THEN RAISE EXCEPTION 'cancelled import requires a terminal outcome'; END IF;
 ELSE
  IF operation_batch IS NOT NULL OR operation_outcome IS NOT NULL OR operation_finished IS NOT NULL OR operation_phase IN ('COMPLETED','FAILED','CANCELLED') THEN RAISE EXCEPTION 'active import cannot contain a terminal outcome'; END IF;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION publication_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.task_type <> 'stock_publication' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'publication outbox history is immutable'; END IF;
 IF (NEW.operation_key,NEW.task_type,NEW.publication_operation_id,NEW.max_attempts) IS DISTINCT FROM (OLD.operation_key,OLD.task_type,OLD.publication_operation_id,OLD.max_attempts) THEN RAISE EXCEPTION 'publication outbox identity is immutable'; END IF;
 IF OLD.state IN ('succeeded','failed','cancelled') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'terminal publication outbox task is immutable'; END IF;
 IF NEW.state<>OLD.state AND NOT ((OLD.state='pending' AND NEW.state IN ('running','failed','cancelled')) OR (OLD.state='running' AND NEW.state IN ('succeeded','failed'))) THEN RAISE EXCEPTION 'publication outbox cannot be replayed'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER publication_outbox_guard BEFORE UPDATE OR DELETE ON outbox_tasks FOR EACH ROW EXECUTE FUNCTION publication_outbox_guard();

ALTER TABLE stock_publication_targets ADD COLUMN acknowledged_at timestamptz;
ALTER TABLE stock_publication_targets ADD COLUMN http_status integer CHECK(http_status IS NULL OR http_status BETWEEN 100 AND 599);
ALTER TABLE stock_publication_targets ADD COLUMN readback_at timestamptz;
ALTER TABLE stock_publication_targets ADD COLUMN readback_quantity text CHECK(readback_quantity IS NULL OR readback_quantity ~ '^[0-9]+$');
ALTER TABLE stock_publication_targets ADD CONSTRAINT stock_publication_readback_pair CHECK ((readback_at IS NULL) = (readback_quantity IS NULL));
CREATE TABLE stock_publication_readbacks (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 target_id bigint NOT NULL REFERENCES stock_publication_targets(id),
 observed_at timestamptz NOT NULL,
 quantity text CHECK(quantity IS NULL OR quantity ~ '^[0-9]+$'),
 outcome text NOT NULL CHECK(outcome IN ('MATCH','MISMATCH','UNAVAILABLE')),
 CHECK ((outcome='UNAVAILABLE') = (quantity IS NULL))
);
CREATE TRIGGER stock_publication_readbacks_immutable BEFORE UPDATE OR DELETE ON stock_publication_readbacks FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
ALTER TABLE stock_publication_targets DROP CONSTRAINT stock_publication_targets_state_check;
ALTER TABLE stock_publication_targets ADD CONSTRAINT stock_publication_targets_state_check CHECK(state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','RECONCILED','SUCCEEDED','FAILED','UNKNOWN','CONFIRMED_EFFECTIVE','CANCELLED','BLOCKED'));
DROP INDEX stock_publication_target_fence;
CREATE UNIQUE INDEX stock_publication_target_fence ON stock_publication_targets(ozon_account,product_id,warehouse_id) WHERE state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stock_publication_target_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'publication history is immutable'; END IF;
 IF (NEW.operation_id,NEW.ozon_account,NEW.product_id,NEW.warehouse_id,NEW.quantity,NEW.decision) IS DISTINCT FROM (OLD.operation_id,OLD.ozon_account,OLD.product_id,OLD.warehouse_id,OLD.quantity,OLD.decision) THEN RAISE EXCEPTION 'publication target and evidence are immutable'; END IF;
 IF OLD.dispatched_at IS NOT NULL AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at THEN RAISE EXCEPTION 'dispatch fence is immutable'; END IF;
 IF OLD.acknowledged_at IS NOT NULL AND NEW.acknowledged_at IS DISTINCT FROM OLD.acknowledged_at THEN RAISE EXCEPTION 'acknowledgement time is immutable'; END IF;
 IF NEW.state<>OLD.state AND NOT (
  (OLD.state='QUEUED' AND NEW.state IN ('DISPATCHING','CANCELLED','BLOCKED')) OR
  (OLD.state='DISPATCHING' AND NEW.state IN ('ACKNOWLEDGED','SUCCEEDED','FAILED','UNKNOWN','BLOCKED','CANCELLED')) OR
  (OLD.state='ACKNOWLEDGED' AND NEW.state='RECONCILED') OR
  (OLD.state='UNKNOWN' AND NEW.state='CONFIRMED_EFFECTIVE')
 ) THEN RAISE EXCEPTION 'invalid publication state transition'; END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM outbox_tasks WHERE task_type='stock_publication') OR EXISTS(SELECT 1 FROM stock_publication_readbacks) OR EXISTS(SELECT 1 FROM stock_publication_targets WHERE state IN ('ACKNOWLEDGED','RECONCILED') OR acknowledged_at IS NOT NULL OR readback_at IS NOT NULL) THEN
  RAISE EXCEPTION 'refusing to discard controlled publication evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER publication_outbox_guard ON outbox_tasks;
DROP FUNCTION publication_outbox_guard();
DROP TABLE stock_publication_readbacks;
DROP INDEX outbox_publication_operation_unique;
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK (task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import'));
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK (
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL));
ALTER TABLE outbox_tasks DROP COLUMN publication_operation_id;
ALTER TABLE stock_publication_targets DROP CONSTRAINT stock_publication_readback_pair;
ALTER TABLE stock_publication_targets DROP CONSTRAINT stock_publication_targets_state_check;
ALTER TABLE stock_publication_targets ADD CONSTRAINT stock_publication_targets_state_check CHECK(state IN ('QUEUED','DISPATCHING','SUCCEEDED','FAILED','UNKNOWN','CONFIRMED_EFFECTIVE','CANCELLED','BLOCKED'));
DROP INDEX stock_publication_target_fence;
CREATE UNIQUE INDEX stock_publication_target_fence ON stock_publication_targets(ozon_account,product_id,warehouse_id) WHERE state IN ('QUEUED','DISPATCHING','UNKNOWN');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stock_publication_target_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'publication history is immutable'; END IF;
 IF (NEW.operation_id,NEW.ozon_account,NEW.product_id,NEW.warehouse_id,NEW.quantity,NEW.decision) IS DISTINCT FROM (OLD.operation_id,OLD.ozon_account,OLD.product_id,OLD.warehouse_id,OLD.quantity,OLD.decision) THEN RAISE EXCEPTION 'publication target and evidence are immutable'; END IF;
 IF OLD.dispatched_at IS NOT NULL AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at THEN RAISE EXCEPTION 'dispatch fence is immutable'; END IF;
 IF NEW.state<>OLD.state AND NOT ((OLD.state='QUEUED' AND NEW.state IN ('DISPATCHING','CANCELLED','BLOCKED')) OR (OLD.state='DISPATCHING' AND NEW.state IN ('SUCCEEDED','FAILED','UNKNOWN','BLOCKED','CANCELLED')) OR (OLD.state='UNKNOWN' AND NEW.state='CONFIRMED_EFFECTIVE')) THEN RAISE EXCEPTION 'invalid publication state transition'; END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
ALTER TABLE stock_publication_targets DROP COLUMN acknowledged_at, DROP COLUMN http_status, DROP COLUMN readback_at, DROP COLUMN readback_quantity;
