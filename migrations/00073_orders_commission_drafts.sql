-- +goose Up
CREATE TABLE orders_finance_observations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 report_year integer NOT NULL CHECK(report_year>=2023), report_month integer NOT NULL CHECK(report_month BETWEEN 1 AND 12),
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 source jsonb NOT NULL CHECK(jsonb_typeof(source)='object' AND octet_length(source::text)<=8388608),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,moysklad_account,ozon_account,report_year,report_month,fingerprint),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE orders_finance_previews (
 id text PRIMARY KEY, tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 config_revision bigint NOT NULL, preview jsonb NOT NULL, actor text NOT NULL, owner_user_id text NOT NULL,
 automatic boolean NOT NULL, created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL CHECK(expires_at>created_at),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE TABLE orders_finance_work (
 id text PRIMARY KEY, tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 report_year integer NOT NULL, report_month integer NOT NULL CHECK(report_month BETWEEN 1 AND 12),
 preview_id text NOT NULL, config_revision bigint NOT NULL,
 state text NOT NULL CHECK(state IN('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN','RECONCILED','BLOCKED','REJECTED')),
 dispatch_count integer NOT NULL DEFAULT 0 CHECK(dispatch_count BETWEEN 0 AND 1),
 work jsonb NOT NULL CHECK(jsonb_typeof(work)='object'),
 actor text NOT NULL, owner_user_id text NOT NULL, request_key text NOT NULL, request_hash text NOT NULL,
 predecessor_work_id text REFERENCES orders_finance_work(id),
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 UNIQUE(tenant_id,moysklad_account,ozon_account,request_key),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,report_year,report_month),
 UNIQUE(predecessor_work_id),
 CHECK((work->>'id'=id AND work->>'state'=state AND (work->>'dispatch_count')::integer=dispatch_count
 AND work->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)
 AND work#>'{plan,scope}'=work->'scope' AND work->>'preview_id'=preview_id
 AND (work->>'config_revision')::bigint=config_revision AND (work#>>'{plan,config_revision}')::bigint=config_revision
 AND work->'credentials'=work#>'{plan,credentials}'
 AND work#>>'{plan,source,period,year}'=report_year::text AND work#>>'{plan,source,period,month}'=report_month::text) IS TRUE),
 CHECK((dispatch_count=0 AND state IN('QUEUED','CLAIMED','BLOCKED','REJECTED')) OR (dispatch_count=1 AND state IN('DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN','RECONCILED','REJECTED'))),
 FOREIGN KEY(preview_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_finance_previews(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE TABLE orders_finance_periods (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 report_year integer NOT NULL, report_month integer NOT NULL CHECK(report_month BETWEEN 1 AND 12),
 source_fingerprint text NOT NULL, current_fingerprint text NOT NULL, source_changed boolean NOT NULL DEFAULT false,
 work_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,report_year,report_month),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,report_year,report_month) REFERENCES orders_finance_work(id,tenant_id,moysklad_account,ozon_account,report_year,report_month)
);
CREATE TABLE orders_finance_attempts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, work_id text NOT NULL REFERENCES orders_finance_work(id),
 phase text NOT NULL, code text NOT NULL CHECK(length(code)<=128), receipt jsonb, observation jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE orders_finance_schedule (
 tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES orders_configs(tenant_id,moysklad_account,ozon_account)
);
ALTER TABLE outbox_tasks ADD COLUMN finance_work_id text, ADD COLUMN finance_tenant_id text,
 ADD COLUMN finance_moysklad_account text, ADD COLUMN finance_ozon_account text;
ALTER TABLE outbox_tasks ADD CONSTRAINT finance_outbox_scope_fk FOREIGN KEY(finance_work_id,finance_tenant_id,finance_moysklad_account,finance_ozon_account) REFERENCES orders_finance_work(id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync','orders_sync','orders_finance'));
-- Preserve the previous payload predicate exactly for all previous task types.
-- +goose StatementBegin
DO $$ DECLARE prior text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO prior FROM pg_constraint WHERE conrelid='outbox_tasks'::regclass AND conname='outbox_task_payload_check';
 IF prior IS NULL OR left(prior,7)<>'CHECK (' THEN RAISE EXCEPTION 'missing prior outbox payload guard'; END IF;
 ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
 EXECUTE 'ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK ((task_type=''orders_finance'' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL AND max_attempts=1 AND attempts<=1) OR (task_type<>''orders_finance'' AND ' || substring(prior FROM 8 FOR length(prior)-8) || '))';
END $$;
-- +goose StatementEnd
ALTER TABLE outbox_tasks ADD CONSTRAINT finance_outbox_payload CHECK(
 (task_type='orders_finance' AND finance_work_id IS NOT NULL AND finance_tenant_id IS NOT NULL AND finance_moysklad_account IS NOT NULL AND finance_ozon_account IS NOT NULL)
 OR (task_type<>'orders_finance' AND finance_work_id IS NULL AND finance_tenant_id IS NULL AND finance_moysklad_account IS NULL AND finance_ozon_account IS NULL));
CREATE UNIQUE INDEX finance_outbox_work ON outbox_tasks(finance_work_id) WHERE task_type='orders_finance';
CREATE INDEX finance_outbox_due ON outbox_tasks(available_at,id) WHERE task_type='orders_finance' AND state IN('pending','running');
CREATE TRIGGER finance_observations_immutable BEFORE UPDATE OR DELETE ON orders_finance_observations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER finance_previews_immutable BEFORE UPDATE OR DELETE ON orders_finance_previews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER finance_attempts_immutable BEFORE UPDATE OR DELETE ON orders_finance_attempts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- +goose StatementBegin
CREATE FUNCTION orders_finance_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'finance work history is immutable'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account,NEW.report_year,NEW.report_month,NEW.preview_id,NEW.config_revision,NEW.actor,NEW.owner_user_id,NEW.request_key,NEW.request_hash,NEW.predecessor_work_id,NEW.created_at,NEW.work->'plan') IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.moysklad_account,OLD.ozon_account,OLD.report_year,OLD.report_month,OLD.preview_id,OLD.config_revision,OLD.actor,OLD.owner_user_id,OLD.request_key,OLD.request_hash,OLD.predecessor_work_id,OLD.created_at,OLD.work->'plan') OR NEW.dispatch_count<OLD.dispatch_count THEN RAISE EXCEPTION 'finance command is immutable'; END IF;
 IF OLD.dispatch_count=1 AND NEW.state IN('QUEUED','CLAIMED') THEN RAISE EXCEPTION 'dispatched finance work is read-only'; END IF;
 IF OLD.state IN('RECONCILED','REJECTED','BLOCKED') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'finance terminal history is immutable'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER finance_work_guard BEFORE UPDATE OR DELETE ON orders_finance_work FOR EACH ROW EXECUTE FUNCTION orders_finance_work_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_finance_work) OR EXISTS(SELECT 1 FROM orders_finance_previews) OR EXISTS(SELECT 1 FROM orders_finance_observations) OR EXISTS(SELECT 1 FROM orders_config_revisions WHERE config->'finance' IS NOT NULL AND config->'finance'<>'null'::jsonb) THEN RAISE EXCEPTION 'finance policy/report history prevents rollback; use a forward fix'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER finance_work_guard ON orders_finance_work;
DROP FUNCTION orders_finance_work_guard();
ALTER TABLE outbox_tasks DROP CONSTRAINT finance_outbox_scope_fk, DROP CONSTRAINT finance_outbox_payload;
DROP INDEX finance_outbox_work,finance_outbox_due;
ALTER TABLE outbox_tasks DROP COLUMN finance_work_id, DROP COLUMN finance_tenant_id, DROP COLUMN finance_moysklad_account, DROP COLUMN finance_ozon_account;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync','orders_sync'));
-- The expanded payload predicate is equivalent to the old predicate once the
-- restored task-type constraint excludes finance; no legacy guard is weakened.
DROP TABLE orders_finance_schedule,orders_finance_attempts,orders_finance_periods,orders_finance_work,orders_finance_previews,orders_finance_observations;
