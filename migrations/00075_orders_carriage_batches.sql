-- +goose Up
CREATE TABLE orders_carriage_previews (
 id text PRIMARY KEY, tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 config_revision bigint NOT NULL, preview jsonb NOT NULL CHECK(jsonb_typeof(preview)='object' AND octet_length(preview::text)<=8388608),
 actor text NOT NULL, owner_user_id text NOT NULL, created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL CHECK(expires_at>created_at),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE TABLE orders_carriage_work (
 id text PRIMARY KEY, tenant_id text NOT NULL, moysklad_account text NOT NULL, ozon_account text NOT NULL,
 delivery_method_id text NOT NULL CHECK(delivery_method_id ~ '^[1-9][0-9]*$'),
 action text NOT NULL CHECK(action IN('CREATE','APPROVE')), carriage_id text NOT NULL DEFAULT '',
 preview_id text NOT NULL, config_revision bigint NOT NULL,
 state text NOT NULL CHECK(state IN('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN','RECONCILED','BLOCKED','REJECTED')),
 dispatch_count integer NOT NULL DEFAULT 0 CHECK(dispatch_count BETWEEN 0 AND 1),
 work jsonb NOT NULL CHECK(jsonb_typeof(work)='object' AND octet_length(work::text)<=8388608),
 actor text NOT NULL, owner_user_id text NOT NULL, request_key text NOT NULL, request_hash text NOT NULL,
 all_ready_consent boolean NOT NULL CHECK(all_ready_consent), physical_readiness_confirmed boolean NOT NULL CHECK(physical_readiness_confirmed),
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 UNIQUE(tenant_id,moysklad_account,ozon_account,request_key), UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 CHECK((work->>'id'=id AND work->>'state'=state AND (work->>'dispatch_count')::integer=dispatch_count
 AND work->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account)
 AND work#>'{plan,scope}'=work->'scope' AND work->>'preview_id'=preview_id
 AND (work->>'config_revision')::bigint=config_revision AND (work#>>'{plan,config_revision}')::bigint=config_revision
 AND work->'credentials'=work#>'{plan,credentials}' AND work#>>'{plan,action}'=action
 AND work#>>'{plan,delivery_method_id}'=delivery_method_id AND COALESCE(work->>'carriage_id','')=carriage_id) IS TRUE),
 CHECK((dispatch_count=0 AND state IN('QUEUED','CLAIMED','BLOCKED','REJECTED')) OR(dispatch_count=1 AND state IN('DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN','RECONCILED'))),
 FOREIGN KEY(preview_id,tenant_id,moysklad_account,ozon_account) REFERENCES orders_carriage_previews(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES orders_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
-- Global provider target ownership spans pairs and tenant transfers. No foreign
-- work contents are returned when a caller encounters this fence.
CREATE TABLE orders_carriage_heads (
 ozon_account text NOT NULL, delivery_method_id text NOT NULL, work_id text NOT NULL UNIQUE REFERENCES orders_carriage_work(id),
 PRIMARY KEY(ozon_account,delivery_method_id)
);
CREATE TABLE orders_carriage_attempts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, work_id text NOT NULL REFERENCES orders_carriage_work(id),
 phase text NOT NULL, code text NOT NULL CHECK(length(code)<=128), receipt jsonb, observation jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE outbox_tasks ADD COLUMN carriage_work_id text, ADD COLUMN carriage_tenant_id text,
 ADD COLUMN carriage_moysklad_account text, ADD COLUMN carriage_ozon_account text;
ALTER TABLE outbox_tasks ADD CONSTRAINT carriage_outbox_scope_fk FOREIGN KEY(carriage_work_id,carriage_tenant_id,carriage_moysklad_account,carriage_ozon_account) REFERENCES orders_carriage_work(id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync','orders_sync','orders_finance','orders_carriage'));
-- +goose StatementBegin
DO $$ DECLARE prior text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO prior FROM pg_constraint WHERE conrelid='outbox_tasks'::regclass AND conname='outbox_task_payload_check';
 IF prior IS NULL OR left(prior,7)<>'CHECK (' THEN RAISE EXCEPTION 'missing prior outbox payload guard'; END IF;
 ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
 EXECUTE 'ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK ((task_type=''orders_carriage'' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL AND finance_work_id IS NULL AND finance_tenant_id IS NULL AND finance_moysklad_account IS NULL AND finance_ozon_account IS NULL AND max_attempts=1 AND attempts<=1) OR (task_type<>''orders_carriage'' AND ' || substring(prior FROM 8 FOR length(prior)-8) || '))';
END $$;
-- +goose StatementEnd
ALTER TABLE outbox_tasks ADD CONSTRAINT carriage_outbox_payload CHECK(
 (task_type='orders_carriage' AND carriage_work_id IS NOT NULL AND carriage_tenant_id IS NOT NULL AND carriage_moysklad_account IS NOT NULL AND carriage_ozon_account IS NOT NULL)
 OR(task_type<>'orders_carriage' AND carriage_work_id IS NULL AND carriage_tenant_id IS NULL AND carriage_moysklad_account IS NULL AND carriage_ozon_account IS NULL));
CREATE UNIQUE INDEX carriage_outbox_work ON outbox_tasks(carriage_work_id) WHERE task_type='orders_carriage';
CREATE INDEX carriage_outbox_due ON outbox_tasks(available_at,id) WHERE task_type='orders_carriage' AND state IN('pending','running');
CREATE TRIGGER carriage_previews_immutable BEFORE UPDATE OR DELETE ON orders_carriage_previews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER carriage_attempts_immutable BEFORE UPDATE OR DELETE ON orders_carriage_attempts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
-- +goose StatementBegin
CREATE FUNCTION orders_carriage_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'carriage work history is immutable'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account,NEW.delivery_method_id,NEW.action,NEW.preview_id,NEW.config_revision,NEW.actor,NEW.owner_user_id,NEW.request_key,NEW.request_hash,NEW.all_ready_consent,NEW.physical_readiness_confirmed,NEW.created_at,NEW.work->'plan') IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.moysklad_account,OLD.ozon_account,OLD.delivery_method_id,OLD.action,OLD.preview_id,OLD.config_revision,OLD.actor,OLD.owner_user_id,OLD.request_key,OLD.request_hash,OLD.all_ready_consent,OLD.physical_readiness_confirmed,OLD.created_at,OLD.work->'plan') OR NEW.dispatch_count<OLD.dispatch_count THEN RAISE EXCEPTION 'carriage command is immutable'; END IF;
 IF OLD.carriage_id<>'' AND NEW.carriage_id<>OLD.carriage_id THEN RAISE EXCEPTION 'carriage identity is immutable'; END IF;
 IF OLD.dispatch_count=0 AND NEW.dispatch_count=1 AND (OLD.state<>'CLAIMED' OR NEW.state<>'DISPATCHING') THEN RAISE EXCEPTION 'carriage attempt requires claimed dispatch admission'; END IF;
 IF OLD.dispatch_count=1 AND NEW.state IN('QUEUED','CLAIMED','BLOCKED','REJECTED') THEN RAISE EXCEPTION 'attempted carriage work is read-only'; END IF;
 IF OLD.state IN('RECONCILED','REJECTED','BLOCKED') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'carriage terminal history is immutable'; END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION orders_carriage_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.task_type='orders_carriage' THEN RAISE EXCEPTION 'carriage outbox history is immutable'; END IF;
  RETURN OLD;
 END IF;
 IF OLD.task_type<>'orders_carriage' AND NEW.task_type<>'orders_carriage' THEN RETURN NEW; END IF;
 IF ROW(NEW.task_type,NEW.carriage_work_id,NEW.carriage_tenant_id,NEW.carriage_moysklad_account,NEW.carriage_ozon_account,NEW.operation_key,NEW.max_attempts) IS DISTINCT FROM ROW(OLD.task_type,OLD.carriage_work_id,OLD.carriage_tenant_id,OLD.carriage_moysklad_account,OLD.carriage_ozon_account,OLD.operation_key,OLD.max_attempts) THEN RAISE EXCEPTION 'carriage outbox payload is immutable'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER carriage_work_guard BEFORE UPDATE OR DELETE ON orders_carriage_work FOR EACH ROW EXECUTE FUNCTION orders_carriage_work_guard();
CREATE TRIGGER carriage_outbox_guard BEFORE UPDATE OR DELETE ON outbox_tasks FOR EACH ROW EXECUTE FUNCTION orders_carriage_outbox_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM orders_carriage_previews) OR EXISTS(SELECT 1 FROM orders_carriage_work) THEN RAISE EXCEPTION 'carriage consent/attempt history prevents rollback; use a forward fix'; END IF;
END $$;
-- +goose StatementEnd
-- Recover the original nested predicate rather than leaving a dead carriage
-- branch referencing finance columns. A later Down73 must retain its guard.
-- +goose StatementBegin
DO $$ DECLARE expression text; tail text; predicate text; anchor text := '(task_type <> ''orders_carriage''::text)'; start_at integer; position_at integer := 1; depth integer := 0; quoted boolean := false; ch text; BEGIN
 SELECT pg_get_expr(conbin,conrelid) INTO expression FROM pg_constraint WHERE conrelid='outbox_tasks'::regclass AND conname='outbox_task_payload_check';
 start_at := position(anchor IN expression);
 IF start_at=0 THEN RAISE EXCEPTION 'carriage payload wrapper is unavailable'; END IF;
 tail := ltrim(substring(expression FROM start_at+length(anchor)));
 IF left(tail,4)<>'AND ' THEN RAISE EXCEPTION 'carriage payload wrapper shape changed'; END IF;
 tail := ltrim(substring(tail FROM 5));
 IF left(tail,1)<>'(' THEN RAISE EXCEPTION 'prior payload predicate is unavailable'; END IF;
 WHILE position_at<=length(tail) LOOP
  ch := substring(tail FROM position_at FOR 1);
  IF ch='''' THEN
   IF quoted AND substring(tail FROM position_at+1 FOR 1)='''' THEN position_at:=position_at+2; CONTINUE; END IF;
   quoted:=NOT quoted;
  ELSIF NOT quoted THEN
   IF ch='(' THEN depth:=depth+1; ELSIF ch=')' THEN depth:=depth-1; END IF;
   IF depth=0 THEN EXIT; END IF;
  END IF;
  position_at:=position_at+1;
 END LOOP;
 IF quoted OR depth<>0 OR substring(tail FROM position_at+1) !~ '^[[:space:])]*$' THEN RAISE EXCEPTION 'prior payload predicate cannot be recovered exactly'; END IF;
 predicate:=substring(tail FROM 1 FOR position_at);
 ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
 EXECUTE 'ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK (' || predicate || ')';
END $$;
-- +goose StatementEnd
DROP TRIGGER carriage_outbox_guard ON outbox_tasks;
DROP TRIGGER carriage_work_guard ON orders_carriage_work;
DROP FUNCTION orders_carriage_outbox_guard(),orders_carriage_work_guard();
ALTER TABLE outbox_tasks DROP CONSTRAINT carriage_outbox_scope_fk,DROP CONSTRAINT carriage_outbox_payload;
DROP INDEX carriage_outbox_work,carriage_outbox_due;
ALTER TABLE outbox_tasks DROP COLUMN carriage_work_id,DROP COLUMN carriage_tenant_id,DROP COLUMN carriage_moysklad_account,DROP COLUMN carriage_ozon_account;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync','orders_sync','orders_finance'));
DROP TABLE orders_carriage_attempts,orders_carriage_heads,orders_carriage_work,orders_carriage_previews;
