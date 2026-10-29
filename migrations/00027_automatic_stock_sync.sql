-- +goose Up
-- The real-account exception remains fixed to the previously registered test
-- accounts. A deployment switch alone cannot create a policy for any customer.
CREATE TABLE stock_auto_configs (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0), enabled boolean NOT NULL,
 next_run_at timestamptz, last_cycle_at timestamptz,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 CHECK(tenant_id='cross-e2e' AND moysklad_account='2928cf35-d30a-11f1-0a80-1b9000000355' AND ozon_account='1549477'),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES stock_sync_settings(tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE stock_auto_config_audits (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 revision bigint NOT NULL, enabled boolean NOT NULL, snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 principal_type text NOT NULL CHECK(principal_type='LOCAL_OPERATOR_SESSION'),
 session_audit_id text NOT NULL CHECK(length(session_audit_id) BETWEEN 1 AND 128),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 16 AND 128), request_hash text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,revision),
 UNIQUE(tenant_id,moysklad_account,ozon_account,idempotency_key),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES stock_auto_configs(tenant_id,moysklad_account,ozon_account)
);
CREATE TRIGGER stock_auto_config_audits_immutable BEFORE UPDATE OR DELETE ON stock_auto_config_audits FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
ALTER TABLE stock_auto_configs ADD CONSTRAINT stock_auto_current_audit FOREIGN KEY(tenant_id,moysklad_account,ozon_account,revision) REFERENCES stock_auto_config_audits(tenant_id,moysklad_account,ozon_account,revision) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE stock_auto_cycles (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('STARTING','READING','WAITING_PUBLICATION','COMPLETE','BLOCKED','CANCELLED')),
 outcome text NOT NULL DEFAULT '', started_at timestamptz NOT NULL, finished_at timestamptz,
 next_action_at timestamptz NOT NULL,
 moysklad_import_id text REFERENCES ozon_import_operations(id),
 ozon_import_id text REFERENCES ozon_import_operations(id),
 publication_id text UNIQUE REFERENCES stock_publications(id),
 reconcile_attempts integer NOT NULL DEFAULT 0 CHECK(reconcile_attempts BETWEEN 0 AND 3),
 metrics jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metrics)='object'),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES stock_auto_config_audits(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE UNIQUE INDEX stock_auto_one_active_cycle ON stock_auto_cycles(tenant_id,moysklad_account,ozon_account) WHERE state IN ('STARTING','READING','WAITING_PUBLICATION');
CREATE INDEX stock_auto_due_cycle ON stock_auto_cycles(next_action_at) WHERE state IN ('STARTING','READING','WAITING_PUBLICATION');

-- Scheduled reads are attributed to the operator-approved policy, not
-- misrepresented as a newly initiated operator session.
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_principal_type_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_principal_type_check CHECK(principal_type IN ('LOCAL_OPERATOR_SESSION','AUTOMATIC_POLICY'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_auto_config_audits) OR EXISTS(SELECT 1 FROM stock_auto_cycles) OR EXISTS(SELECT 1 FROM ozon_import_operations WHERE principal_type='AUTOMATIC_POLICY') THEN
  RAISE EXCEPTION 'refusing to discard automatic synchronization audit';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ozon_import_operations DROP CONSTRAINT ozon_import_operations_principal_type_check;
ALTER TABLE ozon_import_operations ADD CONSTRAINT ozon_import_operations_principal_type_check CHECK(principal_type='LOCAL_OPERATOR_SESSION');
DROP TABLE stock_auto_cycles;
ALTER TABLE stock_auto_configs DROP CONSTRAINT stock_auto_current_audit;
DROP TABLE stock_auto_config_audits;
DROP TABLE stock_auto_configs;
