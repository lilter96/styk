-- +goose Up
-- Catalog work uses the existing outbox, but never its mock write-retry path.
-- Catalog associations describe reviewed catalog intent, not physical stock identity.
CREATE TABLE catalog_sync_configs (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 moysklad_provider text GENERATED ALWAYS AS ('moysklad'::text) STORED,
 ozon_provider text GENERATED ALWAYS AS ('ozon'::text) STORED,
 revision bigint NOT NULL CHECK(revision>0),
 enabled boolean NOT NULL,
 next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_provider,moysklad_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,ozon_provider,ozon_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE TABLE catalog_sync_config_revisions (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 config jsonb NOT NULL CHECK(jsonb_typeof(config)='object'),
 actor text NOT NULL CHECK(length(actor) BETWEEN 1 AND 128),
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,revision),
 UNIQUE(tenant_id,moysklad_account,ozon_account,request_key),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_configs(tenant_id,moysklad_account,ozon_account) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE catalog_sync_configs ADD CONSTRAINT catalog_sync_current_revision_fk
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,revision) REFERENCES catalog_sync_config_revisions(tenant_id,moysklad_account,ozon_account,revision) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX catalog_sync_due ON catalog_sync_configs(next_run_at,tenant_id) WHERE enabled;

CREATE TABLE catalog_sync_associations (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 target_key text NOT NULL CHECK(length(target_key) BETWEEN 1 AND 512),
 source_provider text NOT NULL CHECK(source_provider IN ('moysklad','ozon')),
 source_account text NOT NULL,
 source_kind text NOT NULL,
 source_id text NOT NULL CHECK(length(source_id)>0),
 target_provider text NOT NULL CHECK(target_provider IN ('moysklad','ozon') AND target_provider<>source_provider),
 target_account text NOT NULL,
 target_kind text NOT NULL,
 target_id text NOT NULL DEFAULT '',
 target_offer_id text NOT NULL DEFAULT '',
 mapping_id bigint,
 mapping_version integer,
 mapping_kind text,
 mapping_item_id text,
 mapping_product_id text,
 mapping_offer_id text,
 provisional boolean NOT NULL,
 resolved_identity jsonb CHECK(resolved_identity IS NULL OR jsonb_typeof(resolved_identity)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 resolved_at timestamptz,
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,target_key),
 CHECK(source_account=CASE source_provider WHEN 'moysklad' THEN moysklad_account ELSE ozon_account END),
 CHECK(target_account=CASE target_provider WHEN 'moysklad' THEN moysklad_account ELSE ozon_account END),
 CHECK((provisional AND mapping_id IS NULL AND mapping_version IS NULL AND mapping_kind IS NULL AND mapping_item_id IS NULL AND mapping_product_id IS NULL AND mapping_offer_id IS NULL)
    OR (NOT provisional AND mapping_id IS NOT NULL AND mapping_version IS NOT NULL AND mapping_kind IS NOT NULL AND mapping_item_id IS NOT NULL AND mapping_product_id IS NOT NULL AND mapping_offer_id IS NOT NULL)),
 CHECK((resolved_identity IS NULL)=(resolved_at IS NULL)),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_configs(tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,source_provider,source_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(tenant_id,target_provider,target_account) REFERENCES stock_connections(tenant_id,source,account_id),
 FOREIGN KEY(mapping_id,mapping_version,tenant_id,ozon_account,moysklad_account,mapping_kind,mapping_item_id,mapping_product_id,mapping_offer_id)
 REFERENCES product_mapping_history(mapping_id,version,tenant_id,ozon_account,moysklad_account,target_kind,target_id,ozon_product_id,ozon_offer_id)
);
CREATE UNIQUE INDEX catalog_sync_create_identity ON catalog_sync_associations(tenant_id,target_provider,target_account,source_provider,source_account,source_kind,source_id) WHERE provisional;
CREATE TABLE catalog_sync_enrollments (
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 target_key text NOT NULL,
 enrollment jsonb NOT NULL CHECK(jsonb_typeof(enrollment)='object'),
 PRIMARY KEY(tenant_id,moysklad_account,ozon_account,config_revision,target_key),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES catalog_sync_config_revisions(tenant_id,moysklad_account,ozon_account,revision),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,target_key) REFERENCES catalog_sync_associations(tenant_id,moysklad_account,ozon_account,target_key)
);
CREATE TABLE catalog_sync_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id text NOT NULL,
 provider text NOT NULL CHECK(provider IN ('moysklad','ozon')),
 account_id text NOT NULL,
 entity_kind text NOT NULL,
 entity_id text NOT NULL,
 revision text NOT NULL,
 product jsonb NOT NULL CHECK(jsonb_typeof(product)='object'),
 observed_at timestamptz NOT NULL,
 UNIQUE(id,tenant_id,provider,account_id),
 FOREIGN KEY(tenant_id,provider,account_id) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE INDEX catalog_sync_snapshot_identity ON catalog_sync_snapshots(tenant_id,provider,account_id,entity_kind,entity_id,id DESC);
CREATE TABLE catalog_sync_previews (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 preview jsonb NOT NULL CHECK(jsonb_typeof(preview)='object'),
 fingerprint text NOT NULL,
 actor text NOT NULL CHECK(length(actor) BETWEEN 1 AND 128),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,config_revision),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES catalog_sync_config_revisions(tenant_id,moysklad_account,ozon_account,revision)
);
CREATE INDEX catalog_sync_preview_history ON catalog_sync_previews(tenant_id,moysklad_account,ozon_account,created_at DESC,id);
CREATE TABLE catalog_sync_preview_snapshots (
 preview_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 snapshot_id bigint NOT NULL,
 provider text NOT NULL,
 account_id text NOT NULL,
 PRIMARY KEY(preview_id,snapshot_id),
 CHECK(account_id=CASE provider WHEN 'moysklad' THEN moysklad_account WHEN 'ozon' THEN ozon_account END),
 FOREIGN KEY(preview_id,tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES catalog_sync_previews(id,tenant_id,moysklad_account,ozon_account,config_revision),
 FOREIGN KEY(snapshot_id,tenant_id,provider,account_id) REFERENCES catalog_sync_snapshots(id,tenant_id,provider,account_id)
);
CREATE TABLE catalog_sync_approvals (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 preview_id text NOT NULL,
 actor text NOT NULL CHECK(length(actor) BETWEEN 1 AND 128),
 operator_key text NOT NULL CHECK(length(operator_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 UNIQUE(tenant_id,moysklad_account,ozon_account,operator_key),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,config_revision),
 FOREIGN KEY(preview_id,tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES catalog_sync_previews(id,tenant_id,moysklad_account,ozon_account,config_revision)
);
CREATE TABLE catalog_sync_operations (
 id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 approval_id text NOT NULL UNIQUE,
 preview_id text NOT NULL,
 created_at timestamptz NOT NULL,
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(approval_id,tenant_id,moysklad_account,ozon_account,config_revision) REFERENCES catalog_sync_approvals(id,tenant_id,moysklad_account,ozon_account,config_revision)
);
CREATE INDEX catalog_sync_operation_history ON catalog_sync_operations(tenant_id,moysklad_account,ozon_account,created_at DESC,id DESC);
CREATE TABLE catalog_sync_work (
 id text PRIMARY KEY,
 operation_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 config_revision bigint NOT NULL,
 target_key text NOT NULL,
 target_provider text NOT NULL CHECK(target_provider IN ('moysklad','ozon')),
 target_account text NOT NULL,
 target_identity text NOT NULL CHECK(length(target_identity)>0),
 state text NOT NULL CHECK(state IN ('QUEUED','CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED','RECONCILED','REJECTED','UNKNOWN','BLOCKED')),
 work jsonb NOT NULL CHECK(jsonb_typeof(work)='object'),
 dispatch_count integer NOT NULL DEFAULT 0 CHECK(dispatch_count BETWEEN 0 AND 1),
 dispatched_at timestamptz,
 finished_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK(target_account=CASE target_provider WHEN 'moysklad' THEN moysklad_account ELSE ozon_account END),
 CHECK((dispatch_count=0)=(dispatched_at IS NULL)),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(id,operation_id,tenant_id,moysklad_account,ozon_account),
 UNIQUE(id,tenant_id,moysklad_account,ozon_account,target_key),
 FOREIGN KEY(operation_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_operations(id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(tenant_id,moysklad_account,ozon_account,config_revision,target_key) REFERENCES catalog_sync_enrollments(tenant_id,moysklad_account,ozon_account,config_revision,target_key),
 FOREIGN KEY(tenant_id,target_provider,target_account) REFERENCES stock_connections(tenant_id,source,account_id)
);
CREATE UNIQUE INDEX catalog_sync_active_target ON catalog_sync_work(tenant_id,target_provider,target_account,target_identity) WHERE state IN ('QUEUED','CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED','UNKNOWN');
CREATE INDEX catalog_sync_work_operation ON catalog_sync_work(operation_id,id);
CREATE TABLE catalog_sync_commands (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 command_id text NOT NULL UNIQUE,
 command jsonb NOT NULL CHECK(jsonb_typeof(command)='object'),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE catalog_sync_attempts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 phase text NOT NULL CHECK(phase IN ('DISPATCHING','PROCESSING','ACKNOWLEDGED','UNKNOWN','BLOCKED','RECONCILED','REJECTED')),
 claim_token text NOT NULL,
 safe_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE UNIQUE INDEX catalog_sync_one_dispatch ON catalog_sync_attempts(work_id) WHERE phase='DISPATCHING';
CREATE TABLE catalog_sync_receipts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 task_id text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_work(id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE catalog_sync_baselines (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 target_key text NOT NULL,
 baseline jsonb NOT NULL CHECK(jsonb_typeof(baseline)='object'),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account,target_key) REFERENCES catalog_sync_work(id,tenant_id,moysklad_account,ozon_account,target_key)
);
CREATE INDEX catalog_sync_latest_baseline ON catalog_sync_baselines(tenant_id,moysklad_account,ozon_account,target_key,created_at DESC);

-- Original approvals stay immutable. Verified sibling effects may advance only
-- the revision guards of an unsent command; every preparation is journaled.
CREATE TABLE catalog_sync_rebases (
 work_id text NOT NULL,
 applied_work_id text NOT NULL,
 operation_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 prepared_work jsonb NOT NULL CHECK(jsonb_typeof(prepared_work)='object'),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(work_id,applied_work_id),
 CHECK(work_id<>applied_work_id),
 FOREIGN KEY(work_id,operation_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_work(id,operation_id,tenant_id,moysklad_account,ozon_account),
 FOREIGN KEY(applied_work_id,operation_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_work(id,operation_id,tenant_id,moysklad_account,ozon_account)
);
CREATE TABLE catalog_sync_dispatch_intents (
 work_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 command jsonb NOT NULL CHECK(jsonb_typeof(command)='object'),
 fingerprint text NOT NULL,
 created_at timestamptz NOT NULL,
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account) REFERENCES catalog_sync_work(id,tenant_id,moysklad_account,ozon_account)
);


ALTER TABLE catalog_sync_config_revisions ADD CONSTRAINT catalog_sync_config_revisions_json_scope CHECK (((config#>>'{scope,tenant}')=tenant_id AND (config#>>'{scope,moysklad_account}')=moysklad_account AND (config#>>'{scope,ozon_account}')=ozon_account) IS TRUE);

ALTER TABLE catalog_sync_previews ADD CONSTRAINT catalog_sync_previews_json_scope CHECK (((preview#>>'{scope,tenant}')=tenant_id AND (preview#>>'{scope,moysklad_account}')=moysklad_account AND (preview#>>'{scope,ozon_account}')=ozon_account) IS TRUE);

ALTER TABLE catalog_sync_work ADD CONSTRAINT catalog_sync_work_json_scope CHECK (((work#>>'{scope,tenant}')=tenant_id AND (work#>>'{scope,moysklad_account}')=moysklad_account AND (work#>>'{scope,ozon_account}')=ozon_account) IS TRUE);

ALTER TABLE catalog_sync_commands ADD CONSTRAINT catalog_sync_commands_json_scope CHECK (((command#>>'{scope,tenant}')=tenant_id AND (command#>>'{scope,moysklad_account}')=moysklad_account AND (command#>>'{scope,ozon_account}')=ozon_account) IS TRUE);
ALTER TABLE catalog_sync_config_revisions ADD CONSTRAINT catalog_sync_config_json_revision CHECK (((config->>'revision')=revision::text) IS TRUE);
ALTER TABLE catalog_sync_previews ADD CONSTRAINT catalog_sync_preview_json_identity CHECK (((preview->>'id')=id AND (preview->>'config_revision')=config_revision::text) IS TRUE);
ALTER TABLE catalog_sync_work ADD CONSTRAINT catalog_sync_work_json_identity CHECK (((work->>'id')=id AND (work->>'operation_id')=operation_id AND (work->>'enrollment_id')=target_key AND (work->>'config_revision')=config_revision::text AND (work->>'state')=state) IS TRUE);
ALTER TABLE catalog_sync_commands ADD CONSTRAINT catalog_sync_command_json_identity CHECK (((command->>'id')=command_id) IS TRUE);
ALTER TABLE catalog_sync_snapshots ADD CONSTRAINT catalog_sync_snapshot_json_identity CHECK (((product#>>'{identity,account,provider}')=provider AND (product#>>'{identity,account,id}')=account_id AND (product#>>'{identity,id}')=entity_id AND (product->>'revision')=revision) IS TRUE);

ALTER TABLE outbox_tasks ADD COLUMN catalog_operation_id text;
ALTER TABLE outbox_tasks ADD COLUMN catalog_work_id text;
ALTER TABLE outbox_tasks ADD COLUMN catalog_tenant_id text;
ALTER TABLE outbox_tasks ADD COLUMN catalog_moysklad_account text;
ALTER TABLE outbox_tasks ADD COLUMN catalog_ozon_account text;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_sync_outbox_operation_fk FOREIGN KEY(catalog_work_id,catalog_operation_id,catalog_tenant_id,catalog_moysklad_account,catalog_ozon_account) REFERENCES catalog_sync_work(id,operation_id,tenant_id,moysklad_account,ozon_account);
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication','catalog_sync'));
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK(
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type='stock_publication' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NOT NULL AND max_attempts=1 AND attempts<=1 AND catalog_operation_id IS NULL AND catalog_work_id IS NULL AND catalog_tenant_id IS NULL AND catalog_moysklad_account IS NULL AND catalog_ozon_account IS NULL)
 OR (task_type='catalog_sync' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL AND catalog_operation_id IS NOT NULL AND catalog_work_id IS NOT NULL AND catalog_tenant_id IS NOT NULL AND catalog_moysklad_account IS NOT NULL AND catalog_ozon_account IS NOT NULL AND max_attempts=1 AND attempts<=1));
CREATE UNIQUE INDEX catalog_sync_outbox_operation ON outbox_tasks(catalog_work_id) WHERE catalog_operation_id IS NOT NULL;
CREATE INDEX catalog_sync_claim ON outbox_tasks(available_at,id) WHERE task_type='catalog_sync' AND state IN ('pending','running');

-- +goose StatementBegin
CREATE FUNCTION catalog_sync_actor_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.actor LIKE 'user.%' THEN PERFORM saas_require_actor(NEW.tenant_id,NEW.actor,true); END IF;
 IF NEW.actor LIKE 'MERCHANT_SESSION:%' THEN PERFORM saas_require_actor(NEW.tenant_id,substr(NEW.actor,18),true); END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalog_sync_config_actor BEFORE INSERT ON catalog_sync_config_revisions FOR EACH ROW EXECUTE FUNCTION catalog_sync_actor_guard();
CREATE TRIGGER catalog_sync_approval_actor BEFORE INSERT ON catalog_sync_approvals FOR EACH ROW EXECUTE FUNCTION catalog_sync_actor_guard();

-- +goose StatementBegin
CREATE FUNCTION catalog_sync_config_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'catalog configuration history is immutable' USING ERRCODE='55000'; END IF;
 IF (NEW.tenant_id,NEW.moysklad_account,NEW.ozon_account) IS DISTINCT FROM (OLD.tenant_id,OLD.moysklad_account,OLD.ozon_account) OR NEW.revision NOT IN (OLD.revision,OLD.revision+1) THEN RAISE EXCEPTION 'catalog configuration scope or revision is invalid' USING ERRCODE='55000'; END IF;
 IF NEW.revision=OLD.revision AND NEW.enabled<>OLD.enabled THEN RAISE EXCEPTION 'catalog configuration changes require a revision' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalog_sync_config_guard BEFORE UPDATE OR DELETE ON catalog_sync_configs FOR EACH ROW EXECUTE FUNCTION catalog_sync_config_guard();

-- +goose StatementBegin
CREATE FUNCTION catalog_sync_association_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'catalog association history is immutable' USING ERRCODE='55000'; END IF;
 IF (to_jsonb(NEW)-'resolved_identity'-'resolved_at') IS DISTINCT FROM (to_jsonb(OLD)-'resolved_identity'-'resolved_at') THEN RAISE EXCEPTION 'catalog association identity is immutable' USING ERRCODE='55000'; END IF;
 IF OLD.resolved_identity IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'resolved catalog identity is immutable' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalog_sync_association_guard BEFORE UPDATE OR DELETE ON catalog_sync_associations FOR EACH ROW EXECUTE FUNCTION catalog_sync_association_guard();

-- +goose StatementBegin
CREATE FUNCTION catalog_sync_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'catalog work history is immutable' USING ERRCODE='55000'; END IF;
 IF (to_jsonb(NEW)-'state'-'work'-'dispatch_count'-'dispatched_at'-'finished_at'-'updated_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'work'-'dispatch_count'-'dispatched_at'-'finished_at'-'updated_at') THEN RAISE EXCEPTION 'catalog work identity is immutable' USING ERRCODE='55000'; END IF;
 IF (NEW.work-'state'-'receipt'-'created_target'-'next_run_at'-'code') IS DISTINCT FROM (OLD.work-'state'-'receipt'-'created_target'-'next_run_at'-'code') THEN
  IF OLD.state<>'QUEUED' OR NEW.state<>'QUEUED' OR OLD.dispatch_count<>0
  OR (NEW.work-'command'-'command_fingerprint'-'decision') IS DISTINCT FROM (OLD.work-'command'-'command_fingerprint'-'decision')
  OR ((NEW.work->'command')-'expected_source_revision'-'expected_target_revision') IS DISTINCT FROM ((OLD.work->'command')-'expected_source_revision'-'expected_target_revision')
  OR NOT EXISTS(SELECT 1 FROM catalog_sync_rebases r JOIN catalog_sync_work a ON a.id=r.applied_work_id JOIN catalog_sync_baselines b ON b.work_id=a.id WHERE r.work_id=OLD.id AND r.prepared_work=NEW.work AND a.state='RECONCILED' AND a.operation_id=OLD.operation_id AND a.target_key=OLD.target_key)
  THEN RAISE EXCEPTION 'catalog work intent is immutable except verified unsent sibling revisions' USING ERRCODE='55000'; END IF;
 END IF;
 IF OLD.state IN ('RECONCILED','REJECTED','BLOCKED','UNKNOWN') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'terminal catalog work cannot be replayed' USING ERRCODE='55000'; END IF;
 IF NEW.dispatch_count<OLD.dispatch_count OR (OLD.dispatched_at IS NOT NULL AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at) THEN RAISE EXCEPTION 'catalog dispatch fence is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.state<>OLD.state AND NOT (
  (OLD.state='QUEUED' AND NEW.state IN ('CLAIMED','BLOCKED')) OR
  (OLD.state='CLAIMED' AND NEW.state IN ('DISPATCHING','BLOCKED','UNKNOWN')) OR
  (OLD.state='DISPATCHING' AND NEW.state IN ('PROCESSING','ACKNOWLEDGED','RECONCILED','REJECTED','UNKNOWN','BLOCKED')) OR
  (OLD.state='PROCESSING' AND NEW.state IN ('ACKNOWLEDGED','RECONCILED','REJECTED','UNKNOWN','BLOCKED')) OR
  (OLD.state='ACKNOWLEDGED' AND NEW.state IN ('RECONCILED','REJECTED','UNKNOWN','BLOCKED'))
 ) THEN RAISE EXCEPTION 'invalid catalog work transition' USING ERRCODE='55000'; END IF;
 IF NEW.state='DISPATCHING' AND NEW.dispatch_count<>1 THEN RAISE EXCEPTION 'catalog dispatch requires durable fence' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalog_sync_work_guard BEFORE UPDATE OR DELETE ON catalog_sync_work FOR EACH ROW EXECUTE FUNCTION catalog_sync_work_guard();

-- +goose StatementBegin
CREATE FUNCTION catalog_sync_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.task_type<>'catalog_sync' AND (TG_OP='DELETE' OR NEW.task_type<>'catalog_sync') THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'catalog outbox history is immutable' USING ERRCODE='55000'; END IF;
 IF (NEW.operation_key,NEW.task_type,NEW.catalog_operation_id,NEW.catalog_work_id,NEW.catalog_tenant_id,NEW.catalog_moysklad_account,NEW.catalog_ozon_account,NEW.max_attempts)
 IS DISTINCT FROM (OLD.operation_key,OLD.task_type,OLD.catalog_operation_id,OLD.catalog_work_id,OLD.catalog_tenant_id,OLD.catalog_moysklad_account,OLD.catalog_ozon_account,OLD.max_attempts) THEN RAISE EXCEPTION 'catalog outbox identity is immutable' USING ERRCODE='55000'; END IF;
 IF OLD.state IN ('succeeded','failed','cancelled') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'terminal catalog outbox is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.attempts<OLD.attempts THEN RAISE EXCEPTION 'catalog attempt fence cannot reset' USING ERRCODE='55000'; END IF;
 IF OLD.state='running' AND NEW.state='pending' AND NOT EXISTS(SELECT 1 FROM catalog_sync_work w WHERE w.id=OLD.catalog_work_id AND w.state IN ('PROCESSING','ACKNOWLEDGED')) THEN RAISE EXCEPTION 'only catalog reads may be rescheduled' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalog_sync_outbox_guard BEFORE UPDATE OR DELETE ON outbox_tasks FOR EACH ROW EXECUTE FUNCTION catalog_sync_outbox_guard();

-- +goose StatementBegin
CREATE FUNCTION catalog_sync_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wid text; ws text; qs text; wc integer;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN
  IF NEW.task_type<>'catalog_sync' THEN RETURN NULL; END IF; wid:=NEW.catalog_work_id;
 ELSE wid:=NEW.id; END IF;
 SELECT w.state,w.dispatch_count,q.state INTO ws,wc,qs FROM catalog_sync_work w JOIN outbox_tasks q ON q.catalog_work_id=w.id AND q.task_type='catalog_sync' WHERE w.id=wid;
 IF NOT FOUND THEN RAISE EXCEPTION 'catalog work requires its outbox task' USING ERRCODE='23514'; END IF;
 IF ws='RECONCILED' AND (qs<>'succeeded' OR NOT EXISTS(SELECT 1 FROM catalog_sync_baselines b WHERE b.work_id=wid)) THEN RAISE EXCEPTION 'catalog reconciliation requires baseline and successful task' USING ERRCODE='23514'; END IF;
 IF ws IN ('REJECTED','UNKNOWN','BLOCKED') AND qs<>'failed' THEN RAISE EXCEPTION 'terminal catalog failure requires failed task' USING ERRCODE='23514'; END IF;
 IF ws IN ('QUEUED','CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED') AND qs NOT IN ('pending','running') THEN RAISE EXCEPTION 'active catalog work requires active task' USING ERRCODE='23514'; END IF;
 IF wc=1 AND (NOT EXISTS(SELECT 1 FROM catalog_sync_attempts a WHERE a.work_id=wid AND a.phase='DISPATCHING') OR NOT EXISTS(SELECT 1 FROM catalog_sync_dispatch_intents i WHERE i.work_id=wid)) THEN RAISE EXCEPTION 'catalog dispatch requires immutable attempt' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER catalog_sync_work_consistency AFTER INSERT OR UPDATE ON catalog_sync_work DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION catalog_sync_consistency();
CREATE CONSTRAINT TRIGGER catalog_sync_outbox_consistency AFTER INSERT OR UPDATE ON outbox_tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION catalog_sync_consistency();
CREATE TRIGGER catalog_sync_rebases_immutable BEFORE UPDATE OR DELETE ON catalog_sync_rebases FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_dispatch_intents_immutable BEFORE UPDATE OR DELETE ON catalog_sync_dispatch_intents FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_config_revisions_immutable BEFORE UPDATE OR DELETE ON catalog_sync_config_revisions FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_enrollments_immutable BEFORE UPDATE OR DELETE ON catalog_sync_enrollments FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_snapshots_immutable BEFORE UPDATE OR DELETE ON catalog_sync_snapshots FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_previews_immutable BEFORE UPDATE OR DELETE ON catalog_sync_previews FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_preview_snapshots_immutable BEFORE UPDATE OR DELETE ON catalog_sync_preview_snapshots FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_approvals_immutable BEFORE UPDATE OR DELETE ON catalog_sync_approvals FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_operations_immutable BEFORE UPDATE OR DELETE ON catalog_sync_operations FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_commands_immutable BEFORE UPDATE OR DELETE ON catalog_sync_commands FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_attempts_immutable BEFORE UPDATE OR DELETE ON catalog_sync_attempts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_receipts_immutable BEFORE UPDATE OR DELETE ON catalog_sync_receipts FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_baselines_immutable BEFORE UPDATE OR DELETE ON catalog_sync_baselines FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose Down
-- Downgrade is safe only before catalog evidence exists. Keep all approved
-- intents, receipts, associations and observations once they have been recorded.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM catalog_sync_configs) OR EXISTS(SELECT 1 FROM catalog_sync_snapshots) OR EXISTS(SELECT 1 FROM catalog_sync_operations) OR EXISTS(SELECT 1 FROM outbox_tasks WHERE task_type='catalog_sync') THEN
  RAISE EXCEPTION 'refusing to discard catalog synchronization evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER catalog_sync_outbox_consistency ON outbox_tasks;
DROP TRIGGER catalog_sync_outbox_guard ON outbox_tasks;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_sync_outbox_operation_fk;
DROP INDEX catalog_sync_outbox_operation;
DROP INDEX catalog_sync_claim;
ALTER TABLE outbox_tasks DROP CONSTRAINT outbox_task_payload_check;
ALTER TABLE outbox_tasks DROP CONSTRAINT catalog_task_type_check;
ALTER TABLE outbox_tasks ADD CONSTRAINT catalog_task_type_check CHECK(task_type IN ('mock','ozon_catalog_import','moysklad_catalog_import','stock_publication'));
ALTER TABLE outbox_tasks ADD CONSTRAINT outbox_task_payload_check CHECK(
 (task_type='mock' AND state<>'cancelled' AND observation_id IS NOT NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NULL)
 OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND observation_id IS NULL AND import_operation_id IS NOT NULL AND import_tenant_id IS NOT NULL AND import_account_id IS NOT NULL AND publication_operation_id IS NULL)
 OR (task_type='stock_publication' AND observation_id IS NULL AND import_operation_id IS NULL AND import_tenant_id IS NULL AND import_account_id IS NULL AND publication_operation_id IS NOT NULL AND max_attempts=1 AND attempts<=1));
ALTER TABLE outbox_tasks DROP COLUMN catalog_operation_id, DROP COLUMN catalog_work_id, DROP COLUMN catalog_tenant_id, DROP COLUMN catalog_moysklad_account, DROP COLUMN catalog_ozon_account;
DROP TABLE catalog_sync_dispatch_intents;
DROP TABLE catalog_sync_rebases;
DROP TABLE catalog_sync_baselines;
DROP TABLE catalog_sync_receipts;
DROP TABLE catalog_sync_attempts;
DROP TABLE catalog_sync_commands;
DROP TABLE catalog_sync_work;
DROP TABLE catalog_sync_operations;
DROP TABLE catalog_sync_approvals;
DROP TABLE catalog_sync_preview_snapshots;
DROP TABLE catalog_sync_previews;
DROP TABLE catalog_sync_snapshots;
DROP TABLE catalog_sync_enrollments;
DROP TABLE catalog_sync_associations;
ALTER TABLE catalog_sync_configs DROP CONSTRAINT catalog_sync_current_revision_fk;
DROP TABLE catalog_sync_config_revisions;
DROP TABLE catalog_sync_configs;
DROP FUNCTION catalog_sync_consistency();
DROP FUNCTION catalog_sync_outbox_guard();
DROP FUNCTION catalog_sync_work_guard();
DROP FUNCTION catalog_sync_association_guard();
DROP FUNCTION catalog_sync_config_guard();
DROP FUNCTION catalog_sync_actor_guard();
