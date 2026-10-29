-- +goose Up
CREATE TABLE catalog_sync_readbacks (
 id text PRIMARY KEY CHECK(length(id)=32),
 work_id text NOT NULL,
 tenant_id text NOT NULL,
 moysklad_account text NOT NULL,
 ozon_account text NOT NULL,
 actor text NOT NULL CHECK(length(actor) BETWEEN 1 AND 128),
 request_key text NOT NULL CHECK(length(request_key) BETWEEN 1 AND 128),
 request_hash text NOT NULL CHECK(length(request_hash)=64),
 reason text NOT NULL CHECK(length(reason) BETWEEN 5 AND 1000),
 accepted_warnings boolean NOT NULL,
 original_work jsonb NOT NULL,
 original_hash text NOT NULL CHECK(length(original_hash)=64),
 created_at timestamptz NOT NULL,
 UNIQUE(tenant_id,moysklad_account,ozon_account,actor,request_key),
 UNIQUE(id,work_id),
 FOREIGN KEY(work_id,tenant_id,moysklad_account,ozon_account)
 REFERENCES catalog_sync_work(id,tenant_id,moysklad_account,ozon_account),
 CHECK(original_work->>'id'=work_id AND original_work->>'state'='UNKNOWN'),
 CHECK(original_work->'scope'=jsonb_build_object('tenant',tenant_id,'moysklad_account',moysklad_account,'ozon_account',ozon_account))
);
CREATE TABLE catalog_sync_readback_claims (
 work_id text PRIMARY KEY,
 request_id text NOT NULL UNIQUE,
 token text NOT NULL CHECK(length(token)=32),
 expires_at timestamptz NOT NULL,
 FOREIGN KEY(request_id,work_id) REFERENCES catalog_sync_readbacks(id,work_id)
);
CREATE TABLE catalog_sync_readback_results (
 request_id text PRIMARY KEY,
 work_id text NOT NULL,
 FOREIGN KEY(request_id,work_id) REFERENCES catalog_sync_readbacks(id,work_id),
 result jsonb NOT NULL,
 completed_at timestamptz NOT NULL,
 CHECK(result->>'id'=request_id AND result->>'work_id'=work_id),
 CHECK(result->>'state' IN ('UNKNOWN','RECONCILED')),
 CHECK(result->>'completed'='true')
);
CREATE INDEX catalog_sync_readback_history ON catalog_sync_readbacks(tenant_id,moysklad_account,ozon_account,work_id,created_at);
CREATE TRIGGER catalog_sync_readbacks_immutable BEFORE UPDATE OR DELETE ON catalog_sync_readbacks FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();
CREATE TRIGGER catalog_sync_readback_results_immutable BEFORE UPDATE OR DELETE ON catalog_sync_readback_results FOR EACH ROW EXECUTE FUNCTION stock_reject_mutation();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION catalog_sync_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wid text; ws text; qs text; wc integer;
BEGIN
 IF TG_TABLE_NAME='outbox_tasks' THEN
  IF NEW.task_type<>'catalog_sync' THEN RETURN NULL; END IF; wid:=NEW.catalog_work_id;
 ELSE wid:=NEW.id; END IF;
 SELECT w.state,w.dispatch_count,q.state INTO ws,wc,qs FROM catalog_sync_work w JOIN outbox_tasks q ON q.catalog_work_id=w.id AND q.task_type='catalog_sync' WHERE w.id=wid;
 IF NOT FOUND THEN RAISE EXCEPTION 'catalog work requires its outbox task' USING ERRCODE='23514'; END IF;
 IF ws='RECONCILED' AND (NOT EXISTS(SELECT 1 FROM catalog_sync_baselines b WHERE b.work_id=wid) OR (qs<>'succeeded' AND NOT(qs='failed' AND EXISTS(SELECT 1 FROM catalog_sync_readback_results r WHERE r.work_id=wid AND r.result->>'state'='RECONCILED')))) THEN RAISE EXCEPTION 'catalog reconciliation requires baseline and successful task' USING ERRCODE='23514'; END IF;
 IF ws IN ('REJECTED','UNKNOWN','BLOCKED') AND qs<>'failed' THEN RAISE EXCEPTION 'terminal catalog failure requires failed task' USING ERRCODE='23514'; END IF;
 IF ws IN ('QUEUED','CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED') AND qs NOT IN ('pending','running') THEN RAISE EXCEPTION 'active catalog work requires active task' USING ERRCODE='23514'; END IF;
 IF wc=1 AND (NOT EXISTS(SELECT 1 FROM catalog_sync_attempts a WHERE a.work_id=wid AND a.phase='DISPATCHING') OR NOT EXISTS(SELECT 1 FROM catalog_sync_dispatch_intents i WHERE i.work_id=wid)) THEN RAISE EXCEPTION 'catalog dispatch requires immutable attempt' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION catalog_sync_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE manual_readback boolean := false;
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
 SELECT EXISTS(SELECT 1 FROM catalog_sync_readback_claims l JOIN catalog_sync_readbacks r ON r.id=l.request_id AND r.work_id=l.work_id WHERE l.work_id=OLD.id AND l.expires_at>clock_timestamp() AND l.token=current_setting('styk.catalog_readback_token',true) AND r.original_work=OLD.work) INTO manual_readback;
 IF OLD.state IN ('RECONCILED','REJECTED','BLOCKED','UNKNOWN') AND NEW IS DISTINCT FROM OLD AND NOT(manual_readback AND OLD.state='UNKNOWN' AND NEW.state IN ('UNKNOWN','RECONCILED') AND NEW.dispatch_count=OLD.dispatch_count AND NEW.dispatched_at IS NOT DISTINCT FROM OLD.dispatched_at) THEN RAISE EXCEPTION 'terminal catalog work cannot be replayed' USING ERRCODE='55000'; END IF;
 IF NEW.dispatch_count<OLD.dispatch_count OR (OLD.dispatched_at IS NOT NULL AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at) THEN RAISE EXCEPTION 'catalog dispatch fence is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.state<>OLD.state AND NOT (
  (manual_readback AND OLD.state='UNKNOWN' AND NEW.state='RECONCILED') OR
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

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM catalog_sync_readbacks) THEN
  RAISE EXCEPTION 'catalog readback audit must be retained' USING ERRCODE='55000';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION catalog_sync_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION catalog_sync_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP TABLE catalog_sync_readback_results;
DROP TABLE catalog_sync_readback_claims;
DROP TABLE catalog_sync_readbacks;
