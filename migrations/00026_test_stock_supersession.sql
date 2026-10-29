-- +goose Up
-- Keep historical UNKNOWN rows unchanged while fencing their physical target.
-- A separate authenticated, audited test-only event can release that fence.
CREATE TABLE stock_test_worker_presence (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 worker_id text NOT NULL, expires_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,worker_id)
);
CREATE TABLE stock_publication_recoveries (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 target_id bigint NOT NULL UNIQUE REFERENCES stock_publication_targets(id),
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 product_id text NOT NULL, warehouse_id text NOT NULL,
 readback_id bigint NOT NULL REFERENCES stock_publication_readbacks(id),
 observed_quantity text NOT NULL CHECK(observed_quantity ~ '^[0-9]+$'),
 observed_at timestamptz NOT NULL,
 principal_type text NOT NULL CHECK(principal_type='LOCAL_OPERATOR_SESSION'),
 session_audit_id text NOT NULL CHECK(length(session_audit_id) BETWEEN 1 AND 128),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 16 AND 128),
 request_hash text NOT NULL,
 reason text NOT NULL CHECK(length(reason) BETWEEN 16 AND 1000),
 accepted_uncertainty boolean NOT NULL CHECK(accepted_uncertainty),
 worker_stopped_assertion boolean NOT NULL CHECK(worker_stopped_assertion),
 created_at timestamptz NOT NULL,
 UNIQUE(tenant_id,idempotency_key)
);
CREATE TRIGGER stock_publication_recoveries_immutable BEFORE UPDATE OR DELETE ON stock_publication_recoveries FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TABLE stock_publication_recovery_links (
 recovery_id text PRIMARY KEY REFERENCES stock_publication_recoveries(id),
 new_operation_id text NOT NULL UNIQUE REFERENCES stock_publications(id),
 created_at timestamptz NOT NULL
);
CREATE TRIGGER stock_publication_recovery_links_immutable BEFORE UPDATE OR DELETE ON stock_publication_recovery_links FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

ALTER TABLE stock_publication_targets ADD CONSTRAINT stock_target_fence_identity UNIQUE(id,ozon_account,product_id,warehouse_id);
CREATE TABLE stock_publication_target_fences (
 ozon_account text NOT NULL, product_id text NOT NULL, warehouse_id text NOT NULL,
 target_id bigint NOT NULL UNIQUE,
 PRIMARY KEY(ozon_account,product_id,warehouse_id),
 FOREIGN KEY(target_id,ozon_account,product_id,warehouse_id)
  REFERENCES stock_publication_targets(id,ozon_account,product_id,warehouse_id)
);
INSERT INTO stock_publication_target_fences(ozon_account,product_id,warehouse_id,target_id)
 SELECT ozon_account,product_id,warehouse_id,id FROM stock_publication_targets WHERE state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN');
DROP INDEX stock_publication_target_fence;

-- +goose StatementBegin
CREATE FUNCTION stock_publication_fence_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN') THEN
   INSERT INTO stock_publication_target_fences VALUES(NEW.ozon_account,NEW.product_id,NEW.warehouse_id,NEW.id);
  END IF;
 ELSIF OLD.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN') AND NEW.state NOT IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN') THEN
  DELETE FROM stock_publication_target_fences WHERE target_id=NEW.id;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_publication_fence_change AFTER INSERT OR UPDATE OF state ON stock_publication_targets FOR EACH ROW EXECUTE FUNCTION stock_publication_fence_change();

-- +goose StatementBegin
CREATE FUNCTION stock_publication_fence_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM stock_publication_targets WHERE id=OLD.target_id AND state='UNKNOWN')
  AND NOT EXISTS(SELECT 1 FROM stock_publication_recoveries WHERE target_id=OLD.target_id) THEN
  RAISE EXCEPTION 'unknown stock target requires audited supersession';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_publication_fence_guard BEFORE DELETE ON stock_publication_target_fences FOR EACH ROW EXECUTE FUNCTION stock_publication_fence_guard();

-- +goose StatementBegin
CREATE FUNCTION stock_test_recovery_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id
  JOIN stock_publication_readbacks r ON r.id=NEW.readback_id AND r.target_id=t.id
  JOIN outbox_tasks q ON q.publication_operation_id=p.id
  JOIN stock_publication_target_fences f ON f.target_id=t.id
  WHERE t.id=NEW.target_id AND t.state='UNKNOWN' AND q.state='failed' AND q.attempts=1
   AND p.tenant_id=NEW.tenant_id AND p.moysklad_account=NEW.moysklad_account AND p.ozon_account=NEW.ozon_account
   AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id
   AND NEW.tenant_id='cross-e2e' AND NEW.moysklad_account='2928cf35-d30a-11f1-0a80-1b9000000355'
   AND NEW.ozon_account='1549477' AND NEW.product_id='2764544165' AND NEW.warehouse_id='1020001323428000'
   AND r.quantity=NEW.observed_quantity AND r.observed_at=NEW.observed_at AND r.outcome IN ('MATCH','MISMATCH')
   AND r.id=(SELECT id FROM stock_publication_readbacks WHERE target_id=t.id ORDER BY observed_at DESC,id DESC LIMIT 1)
   AND NEW.created_at>=r.observed_at AND NEW.created_at-r.observed_at<=interval '2 minutes'
 ) THEN RAISE EXCEPTION 'invalid scoped recovery evidence'; END IF;
 IF EXISTS(SELECT 1 FROM stock_test_worker_presence w WHERE w.tenant_id=NEW.tenant_id AND w.moysklad_account=NEW.moysklad_account AND w.ozon_account=NEW.ozon_account AND w.expires_at>NEW.created_at)
  OR EXISTS(SELECT 1 FROM stock_publication_targets t WHERE t.ozon_account=NEW.ozon_account AND t.product_id=NEW.product_id AND t.warehouse_id=NEW.warehouse_id AND t.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED')) THEN
  RAISE EXCEPTION 'active test worker or dispatch blocks recovery';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_test_recovery_guard BEFORE INSERT ON stock_publication_recoveries FOR EACH ROW EXECUTE FUNCTION stock_test_recovery_guard();

-- +goose StatementBegin
CREATE FUNCTION stock_recovered_original_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state='UNKNOWN' AND EXISTS(SELECT 1 FROM stock_publication_recoveries WHERE target_id=OLD.id) AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'superseded unknown publication is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_recovered_original_guard BEFORE UPDATE ON stock_publication_targets FOR EACH ROW EXECUTE FUNCTION stock_recovered_original_guard();

-- +goose StatementBegin
CREATE FUNCTION stock_test_recovery_link_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM stock_publication_recoveries r JOIN stock_publications p ON p.id=NEW.new_operation_id JOIN stock_sync_reviews v ON v.id=p.review_id JOIN stock_publication_targets t ON t.operation_id=p.id WHERE r.id=NEW.recovery_id AND p.tenant_id=r.tenant_id AND p.moysklad_account=r.moysklad_account AND p.ozon_account=r.ozon_account AND t.product_id=r.product_id AND t.warehouse_id=r.warehouse_id AND v.created_at>r.created_at AND p.created_at>=v.created_at) THEN
  RAISE EXCEPTION 'recovery link requires a fresh scoped publication';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stock_test_recovery_link_guard BEFORE INSERT ON stock_publication_recovery_links FOR EACH ROW EXECUTE FUNCTION stock_test_recovery_link_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_publication_recoveries) THEN RAISE EXCEPTION 'refusing to discard recovery audit'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER stock_test_recovery_guard ON stock_publication_recoveries;
DROP FUNCTION stock_test_recovery_guard();
DROP TRIGGER stock_recovered_original_guard ON stock_publication_targets;
DROP FUNCTION stock_recovered_original_guard();
DROP TRIGGER stock_test_recovery_link_guard ON stock_publication_recovery_links;
DROP FUNCTION stock_test_recovery_link_guard();
DROP TRIGGER stock_publication_fence_change ON stock_publication_targets;
DROP FUNCTION stock_publication_fence_change();
DROP TRIGGER stock_publication_fence_guard ON stock_publication_target_fences;
DROP FUNCTION stock_publication_fence_guard();
DROP TABLE stock_publication_target_fences;
ALTER TABLE stock_publication_targets DROP CONSTRAINT stock_target_fence_identity;
CREATE UNIQUE INDEX stock_publication_target_fence ON stock_publication_targets(ozon_account,product_id,warehouse_id) WHERE state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN');
DROP TABLE stock_publication_recovery_links;
DROP TABLE stock_publication_recoveries;
DROP TABLE stock_test_worker_presence;
